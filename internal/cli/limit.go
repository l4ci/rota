package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/keepalive"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// D3 (#67): `rota limit watch|status`. The loop is internal/limits; this file
// wires it to the host, the account meter, `round transfer` and the escalation
// entry, and is the process side of `watch`. The same wiring runs inside
// `rota keepalive run` (limitsLoop).

func limitCommands() *Command {
	return &Command{Name: "limit", Summary: "usage limits: sleep until the reset or switch accounts", Subs: []*Command{
		{Name: "watch", Summary: "watch the orchestrator and the slots for a usage limit and handle it", Verb: limitWatch},
		{Name: "status", Summary: "show the usage-limit log", Verb: noFlags(limitStatus)},
	}}
}

// escalateFunc posts on an issue thread through C4's library entry.
func escalateFunc(ctx context.Context, c *Ctx, root string) func(issue tracker.IssueKey, title, body string) (string, []string, error) {
	return func(issue tracker.IssueKey, title, body string) (string, []string, error) {
		ee := c.deps().escalationEnv()
		if ee.Forge == nil {
			ee.Forge = escalationForge(c)
		}
		res, err := escalation.Send(context.WithoutCancel(ctx), ee, root, escalation.SendOpts{Key: issue, Title: title, Body: body})
		if err != nil {
			return "", res.Warnings, err
		}
		return res.Entry.ID, res.Warnings, nil
	}
}

// orchestratorData reads the orchestrator's rate limits from the D1 session
// files: the newest one whose cwd is the project root (not a slot's worktree).
// A file older than hook.StateMaxAgeSeconds that shows no limit is not
// evidence, so it reads as unknown.
func orchestratorData(commonDir, root string, maxAge time.Duration) func(now time.Time) limits.Data {
	return func(now time.Time) limits.Data {
		best, bestAt, ok := hook.NewestAtRoot(commonDir, root, func(st hook.State) (string, bool) { return st.UpdatedAt, true })
		if !ok {
			return limits.Data{}
		}
		d := limits.DataFromRateLimits(best.RateLimits, now)
		if !d.Limited && maxAge > 0 && now.Sub(bestAt) > maxAge {
			return limits.Data{}
		}
		return d
	}
}

// limitRig is a watcher with what it needs to run.
type limitRig struct {
	W      *limits.Watcher
	Feed   <-chan limits.Match
	Cancel func()
}

// buildLimits wires a watcher to the host and the registry. A host that is
// unavailable, or herdr outside 0.9.x, is an *Error with exit 5. A refused or
// failed herdr subscription is not: the watcher captures the panes instead,
// and the rig says so in warn.
func buildLimits(ctx context.Context, c *Ctx, root string, cfg any, set limits.Settings, holderPID int, tick time.Duration, gap *atomic.Bool, warn func(string, ...any)) (*limitRig, error) {
	kind := c.deps().resolvePaneHost(root, cfg)
	if kind == host.Solo {
		kind = host.Tmux // New maps solo to tmux; say so for the error text
	}
	h := c.deps().Host(kind)
	if err := h.Require(); err != nil {
		return nil, &Error{Exit: ExitUnavailable, Message: err.Error()}
	}
	ph := h.Capabilities().PaneHost
	if ph == nil {
		return nil, &Error{Exit: ExitUnavailable, Message: kind.String() + " cannot address panes"}
	}
	cd, err := rotastate.CommonDirVia(c.Context(), c.deps().Git, root)
	if err != nil {
		return nil, &Error{Exit: ExitUnavailable, Message: err.Error()}
	}
	maxAge := time.Duration(0)
	if hs, err := hook.LoadSettings(cfg); err == nil {
		maxAge = time.Duration(hs.StateMaxAge) * time.Second
	}
	src := accountSource{c: c, root: root}
	orchPane := host.CurrentPane(kind, c.deps().Getenv)

	var mu sync.Mutex
	panes := map[string]string{} // slot|handle -> pane
	targets := func(ctx context.Context) []limits.Target {
		var out []limits.Target
		if orchPane != "" {
			out = append(out, limits.Target{Session: limits.Orchestrator, Pane: orchPane, Orchestrator: true})
		}
		// Tolerant: a limit-watch target list is rebuilt every tick and cannot refuse.
		for _, s := range worker.LoadRegistryTolerant(root).Slots() {
			name, handle := s.Name(), s.Handle()
			if name == "" || handle == "" {
				continue
			}
			key := name + "|" + handle
			mu.Lock()
			pane := panes[key]
			mu.Unlock()
			if pane == "" {
				pane = ph.PaneOf(ctx, name, handle)
				if pane == "" {
					continue
				}
				mu.Lock()
				panes[key] = pane
				mu.Unlock()
			}
			kind, account := round.SlotAccount(s)
			out = append(out, limits.Target{Session: name, Pane: pane, Account: account, Kind: kind,
				Issue: s.HeldID()})
		}
		return out
	}

	d := limits.Deps{
		Root: root, Settings: set, Now: c.deps().Now, Tick: tick,
		InGap:   func() bool { return gap != nil && gap.Load() },
		Ledger:  func(e ledger.Entry) { worker.LedgerNote(c.deps().workerEnv(), root, e) },
		Move:    limitMover{c: c, root: root, holderPID: holderPID},
		Targets: targets,
		Capture: func(ctx context.Context, t limits.Target) string {
			return ph.CapturePane(ctx, t.Pane, 60)
		},
		OrchestratorData: orchestratorData(cd, root, maxAge),
		Meter: func(ctx context.Context, account string) limits.Reading {
			return limits.MeterReading(ctx, src, account)
		},
		PickAccount: func(ctx context.Context, kind, exclude string) (string, bool) {
			return limits.PickAccount(ctx, src, root, kind, exclude, c.deps().Now())
		},
		Send: func(ctx context.Context, t limits.Target, prompt string) error {
			return ph.SendPane(ctx, t.Pane, prompt)
		},
		Escalate: escalateFunc(ctx, c, root),
		Notify:   func(title, body string) { keepaliveNotify(context.WithoutCancel(ctx), c, root, title, body) },
	}
	rig := &limitRig{Cancel: func() {}}

	var feed *limits.Feed
	if kind == host.Herdr {
		ow := h.Capabilities().OutputWatcher
		if ow == nil {
			return nil, &Error{Exit: ExitUnavailable, Message: "herdr cannot watch pane output"}
		}
		f, err := limits.NewFeed(ctx, ow, targets, worker.LimitRegex(), tick)
		switch {
		case errors.Is(err, host.ErrUnsupportedHerdr):
			return nil, &Error{Exit: ExitUnavailable, Message: err.Error()}
		case err != nil:
			warn("herdr pane.output_matched is not available (%v); capturing the panes every %s instead", err, tick)
			d.Poll = true
		default:
			feed = f
			rig.Feed, rig.Cancel = f.Matches(), f.Stop
		}
	} else {
		d.Poll = true
	}
	rig.W = limits.New(d)
	if feed != nil {
		feed.OnDegrade = func(err error) {
			warn("herdr output events stopped (%v); capturing the panes every %s instead", err, tick)
			rig.W.SetPoll(true)
		}
		feed.Start()
	}
	return rig, nil
}

// limitMover satisfies limits.Mover over the round registry and transfer.
type limitMover struct {
	c         *Ctx
	root      string
	holderPID int
}

func (m limitMover) IdleSlot(_ context.Context, kind, account string) (string, bool) {
	rc, ok := m.c.deps().tolerantRoundSettings(m.root)
	if !ok {
		return "", false
	}
	// Tolerant: no idle slot is found in a corrupt registry (callback, no error return).
	return round.IdleSlotInRound(worker.LoadRegistryTolerant(m.root), rc, kind, account)
}

func (m limitMover) Transfer(ctx context.Context, issue, to string) error {
	env, be, err := moveEnv(m.c, m.root)
	if err != nil {
		return err
	}
	env.HolderPID = m.holderPID
	// A move that has begun is finished, not cut by --timeout or a
	// signal: it leaves a claim and a branch half-moved otherwise (the
	// same call would resume it, but nobody is there to make it).
	_, err = env.Transfer(context.WithoutCancel(ctx), m.root, be, round.TransferOpts{
		Issue: issue, To: to,
		Note: "The slot's account hit its usage limit; rota limit watch moved the issue to an idle slot on another account.",
	})
	return err
}

// accountSource adapts the worker accounts and the harness config to
// limits.AccountSource.
type accountSource struct {
	c    *Ctx
	root string
}

func (a accountSource) Logins(kind string) []string {
	var logins []string
	for _, e := range harness.Resolve(kind).Accounts(a.c.deps().Config(a.root)).Entries {
		logins = append(logins, e.Name)
	}
	return logins
}

func (a accountSource) Meters(ctx context.Context) []worker.Meter {
	return a.c.deps().WorkerAccounts().Meters(ctx, a.root)
}

func (a accountSource) Pick(ctx context.Context, exclude []string) (string, bool) {
	return a.c.deps().WorkerAccounts().Pick(ctx, a.root, exclude)
}

// limitsLoop is the loop the supervisor runs beside its child. Failures are
// warnings: the supervisor's own job goes on without it.
func limitsLoop(c *Ctx, root string, cfg any, set limits.Settings, holderPID int, gap *atomic.Bool) func(ctx context.Context) []string {
	return func(ctx context.Context) []string {
		var warns []string
		warn := func(format string, a ...any) { warns = append(warns, fmt.Sprintf(format, a...)) }
		rig, err := buildLimits(ctx, c, root, cfg, set, holderPID, 5*time.Second, gap, warn)
		if err != nil {
			warn("usage-limit watcher not started: %v", err)
			return warns
		}
		defer rig.Cancel()
		if cd, err := rotastate.CommonDirVia(ctx, c.deps().Git, root); err == nil {
			rec := ownWatching(c, limits.ModeSupervisor)
			if err := limits.WriteWatching(cd, rec); err == nil {
				defer limits.RemoveWatching(cd, rec.PID)
			}
		}
		rig.W.Run(ctx, rig.Feed)
		return append(warns, rig.W.Warnings()...)
	}
}

// ---- rota limit watch -----------------------------------------------------------

func limitWatch(fs *flag.FlagSet) RunFunc {
	timeout := fs.Float64("timeout", 0, "seconds to watch after the first full pass before stopping; 0 watches until interrupted")
	settle := fs.Float64("settle", 5, "seconds between pane captures (tmux, and the fallback poll)")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		if *timeout < 0 {
			return Result{}, Usage("--timeout must not be negative")
		}
		if *settle <= 0 {
			return Result{}, Usage("--settle must be more than zero")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		cfg := c.deps().Config(root)
		set, err := limits.LoadSettings(cfg)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error(), Hint: "fix the limits.* key with: rota config set"}
		}
		cd, err := rotastate.CommonDirVia(c.Context(), c.deps().Git, root)
		if err != nil {
			return Result{}, &Error{Exit: ExitUnavailable, Message: err.Error()}
		}
		if r, err := limitWatchGuard(c, root, cd); err != nil {
			return r, err
		}

		ctx := c.Context()
		tick := time.Duration(*settle * float64(time.Second))
		rig, err := buildLimits(ctx, c, root, cfg, set, c.deps().HolderPID(), tick, nil, func(f string, a ...any) { c.Warn(f, a...) })
		if err != nil {
			return Result{}, err
		}
		defer rig.Cancel()
		rec := ownWatching(c, limits.ModeWatch)
		if err := limits.WriteWatching(cd, rec); err != nil {
			c.Warn("watcher record not written: %v", err)
		} else {
			defer limits.RemoveWatching(cd, rec.PID)
		}

		before := limitsJSON(root)
		rig.W.RunFor(ctx, rig.Feed, time.Duration(*timeout*float64(time.Second)))
		for _, w := range rig.W.Warnings() {
			c.Warn("%s", w)
		}

		list := limits.Load(root)
		d, text := limitsData(list)
		d.Set("changed", limitsJSON(root) != before)
		var res error
		if rig.W.Failed() > 0 {
			res = Failed("a usage limit could not be resolved")
		}
		return Result{Data: d, Text: text}, res
	}
}

// ownWatching is this process's watcher record, stamped so its pid can be told
// from a reused one.
func ownWatching(c *Ctx, mode string) limits.Watching {
	pid := os.Getpid()
	host, start := c.deps().LeaseEnv().Identity(pid)
	return limits.Watching{PID: pid, Host: host, Start: start, StartedAt: limits.Time(c.deps().Now()), Mode: mode}
}

// limitWatchGuard refuses a watcher that would act twice or without
// standing: a live supervisor, another watcher, or a caller without the lease.
func limitWatchGuard(c *Ctx, root, cd string) (Result, error) {
	le := c.deps().LeaseEnv()
	lease, st, held, err := round.HoldsLease(round.LeaseProbe{Env: le, HolderPID: c.deps().HolderPID(), Getenv: c.deps().Getenv}, cd)
	if err != nil {
		return Result{}, &Error{Exit: ExitUnavailable, Message: err.Error()}
	}
	refuse := func(by, msg, hint string) (Result, error) {
		d := jsonx.NewObject()
		d.Set("blockedBy", by)
		d.Set("changed", false)
		return Result{Data: d}, &Error{Exit: ExitRefused, Message: msg, Hint: hint}
	}
	if ks, found, _ := keepalive.ReadState(keepalive.StatePath(cd)); st == roundlease.Live && found && ks.Running(le) && lease.PID == ks.PID {
		return refuse(blockedby.Supervised, fmt.Sprintf("rota keepalive run (pid %d) holds the lease and already watches for usage limits", ks.PID),
			"run rota limit status, or start the supervisor with --no-limits to watch by hand")
	}
	if !held {
		return refuse(blockedby.NoRound, "this process holds no round lease: run rota round start first", "a switch moves work, which is the orchestrator's act")
	}
	if w, ok := limits.ActiveWatching(le, cd); ok && w.PID != os.Getpid() {
		return refuse(blockedby.Watching, fmt.Sprintf("a usage-limit watcher (%s, pid %d) is already running", w.Mode, w.PID), "one watcher acts on the limits list")
	}
	return Result{}, nil
}

// ---- data -----------------------------------------------------------------------

func limitsJSON(root string) string {
	b, _ := jsonx.MarshalCompact(limitRows(limits.Load(root)))
	return string(b)
}

func limitRows(list []limits.Entry) []any {
	rows := make([]any, 0, len(list))
	for _, e := range list {
		rows = append(rows, e.Object())
	}
	return rows
}

func limitLine(e limits.Entry) string {
	reset := "-"
	if e.ResetsAt != "" {
		reset = e.ResetsAt
	}
	return strings.Join([]string{e.ID, e.Status, e.Session, e.Window, reset, e.Action, e.Note}, "\t")
}

// limitsData is watch's data without changed, and its text.
func limitsData(list []limits.Entry) (*jsonx.Object, string) {
	counts := map[string]int{}
	var lines []string
	for _, e := range list {
		counts[e.Status]++
		lines = append(lines, limitLine(e))
	}
	d := jsonx.NewObject()
	d.Set("limits", limitRows(list))
	d.Set("waiting", counts[limits.StatusWaiting])
	d.Set("resumed", counts[limits.StatusResumed])
	d.Set("switched", counts[limits.StatusSwitched])
	d.Set("failed", counts[limits.StatusFailed])
	text := strings.Join(lines, "\n")
	if text == "" {
		text = "no usage limits recorded"
	}
	return d, text
}

// ---- rota limit status --------------------------------------------------------------

func limitStatus(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	list := limits.Load(root)
	watching := false
	if cd, err := rotastate.CommonDirVia(c.Context(), c.deps().Git, root); err == nil {
		le := c.deps().LeaseEnv()
		if _, ok := limits.ActiveWatching(le, cd); ok {
			watching = true
		}
	}
	d := jsonx.NewObject()
	d.Set("limits", limitRows(list))
	d.Set("watching", watching)
	var lines []string
	for _, e := range list {
		lines = append(lines, limitLine(e))
	}
	if watching {
		lines = append(lines, "watching")
	}
	text := strings.Join(lines, "\n")
	if text == "" {
		text = "no usage limits recorded"
	}
	return Result{Data: d, Text: text}, nil
}

// limitStatusRows is the waiting limits as data rows and text lines, for
// round status and reconcile.
func limitStatusRows(list []limits.Entry) ([]any, []string) {
	var lines []string
	for _, e := range list {
		lines = append(lines, limitLine(e))
	}
	return limitRows(list), lines
}
