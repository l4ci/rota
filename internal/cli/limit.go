package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/keepalive"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
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

// limitHost builds the host for a kind; tests replace it.
var limitHost = func(kind string) host.Host { return host.New(kind, host.Deps{}) }

// limitHostKind is herdr when work.dispatch says so or the process runs inside
// herdr, else tmux, as round status chooses.
func limitHostKind(cfg any) string {
	if v, err := config.Value(cfg, "work.dispatch"); err == nil {
		if s, _ := v.(string); s == "herdr" {
			return "herdr"
		}
	}
	if os.Getenv("HERDR_ENV") == "1" {
		return "herdr"
	}
	return "tmux"
}

// escalateFunc posts on an issue thread through C4's library entry.
func escalateFunc(ctx context.Context, root string) func(issue int, title, body string) (string, []string, error) {
	return func(issue int, title, body string) (string, []string, error) {
		ee := escalationEnv()
		if ee.Forge == nil {
			ee.Forge = escalationForge()
		}
		res, err := escalation.Send(context.WithoutCancel(ctx), ee, root, escalation.SendOpts{Number: issue, Title: title, Body: body})
		if err != nil {
			return "", res.Warnings, err
		}
		return res.Entry.ID, res.Warnings, nil
	}
}

// orchestratorData reads the orchestrator's rate limits from the D1 session
// files: the newest one whose cwd is the project root (not a slot's worktree).
// A file older than orchestrator.stateMaxAgeSeconds that shows no limit is not
// evidence, so it reads as unknown.
func orchestratorData(commonDir, root string, maxAge time.Duration) func(now time.Time) limits.Data {
	return func(now time.Time) limits.Data {
		ents, err := os.ReadDir(filepath.Join(commonDir, "rota", "session"))
		if err != nil {
			return limits.Data{}
		}
		var best hook.State
		var bestAt time.Time
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			st, found, err := hook.ReadState(filepath.Join(commonDir, "rota", "session", e.Name()))
			if err != nil || !found || !sameDir(st.Cwd, root) {
				continue
			}
			at, err := time.Parse(time.RFC3339, st.UpdatedAt)
			if err != nil {
				continue
			}
			if bestAt.IsZero() || at.After(bestAt) {
				best, bestAt = st, at
			}
		}
		if bestAt.IsZero() {
			return limits.Data{}
		}
		d := limits.DataFromRateLimits(best.RateLimits, now)
		if !d.Limited && maxAge > 0 && now.Sub(bestAt) > maxAge {
			return limits.Data{}
		}
		return d
	}
}

// inGap is whether the supervisor has no orchestrator child right now (D4).
func inGap(gap *atomic.Bool) bool { return gap != nil && gap.Load() }

// gapped hides the orchestrator's session file while there is no child: a
// limit read from the last session must not type a resume prompt at a
// supervisor with nothing to receive it.
func gapped(gap *atomic.Bool, f func(time.Time) limits.Data) func(time.Time) limits.Data {
	return func(now time.Time) limits.Data {
		if inGap(gap) {
			return limits.Data{}
		}
		return f(now)
	}
}

func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
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
	kind := limitHostKind(cfg)
	h := limitHost(kind)
	if err := h.Require(); err != nil {
		return nil, &Error{Exit: ExitUnavailable, Message: err.Error()}
	}
	ph, ok := h.(host.PaneHost)
	if !ok {
		return nil, &Error{Exit: ExitUnavailable, Message: kind + " cannot address panes"}
	}
	cd, err := roundlease.CommonDir(root)
	if err != nil {
		return nil, &Error{Exit: ExitUnavailable, Message: err.Error()}
	}
	maxAge := time.Duration(0)
	if hs, err := hook.LoadSettings(cfg); err == nil {
		maxAge = time.Duration(hs.StateMaxAge) * time.Second
	}
	accounts := workerAccounts
	orchPane := os.Getenv("TMUX_PANE")
	if kind == "herdr" {
		orchPane = os.Getenv("HERDR_PANE_ID")
	}

	var mu sync.Mutex
	panes := map[string]string{} // slot|handle -> pane
	targets := func(ctx context.Context) []limits.Target {
		var out []limits.Target
		if orchPane != "" {
			out = append(out, limits.Target{Session: limits.Orchestrator, Pane: orchPane, Orchestrator: true})
		}
		for _, s := range worker.LoadRegistry(root).Slots() {
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
			out = append(out, limits.Target{Session: name, Pane: pane, Account: s.Account(),
				Issue: round.SlotIssue(s.Task(), s.Branch(), name)})
		}
		return out
	}

	d := limits.Deps{
		Root: root, Settings: set, Now: hookNow, Tick: tick,
		Targets: targets,
		Capture: func(ctx context.Context, t limits.Target) string {
			if t.Orchestrator && inGap(gap) {
				return ""
			}
			return ph.CapturePane(ctx, t.Pane, 60)
		},
		OrchestratorData: gapped(gap, orchestratorData(cd, root, maxAge)),
		Meter: func(ctx context.Context, account string) limits.Reading {
			for _, m := range accounts().Meters(ctx, root) {
				if m.Name != account {
					continue
				}
				r := limits.Reading{Known: m.Verdict != worker.VerdictUnknown, Cooling: m.Verdict == worker.VerdictCooling, Window: limits.WindowUnknown}
				if m.ResetsAt != nil {
					r.ResetsAt = m.ResetsAt.UTC()
				}
				switch {
				case m.SevenDay != nil && *m.SevenDay >= 100:
					r.Window = limits.WindowSevenDay
				case m.FiveHour != nil && *m.FiveHour >= 100:
					r.Window = limits.WindowFiveHour
				}
				return r
			}
			return limits.Reading{}
		},
		PickAccount: func(ctx context.Context, exclude string) (string, bool) {
			return accounts().Pick(ctx, root, []string{exclude})
		},
		IdleSlot: func(ctx context.Context, account string) (string, bool) {
			rc, err := roundcfg.Load(root)
			if err != nil {
				return "", false
			}
			for _, s := range worker.LoadRegistry(root).Slots() {
				name := s.Name()
				if s.Account() != account || !inList(rc.Roster, name) {
					continue
				}
				if round.SlotIssue(s.Task(), s.Branch(), name) == "" && s.State() != "busy" {
					return name, true
				}
			}
			return "", false
		},
		Transfer: func(ctx context.Context, issue, to string) error {
			rc, err := roundcfg.Load(root)
			if err != nil {
				return err
			}
			env, be, err := moveEnv(c, root)
			if err != nil {
				return err
			}
			// A move that has begun is finished, not cut by --timeout or a
			// signal: it leaves a claim and a branch half-moved otherwise (the
			// same call would resume it, but nobody is there to make it).
			_, err = env.Transfer(context.WithoutCancel(ctx), root, be, round.TransferOpts{
				Issue: issue, To: to, HolderPID: holderPID, Settings: rc, Getenv: os.Getenv,
				Note: "The slot's account hit its usage limit; rota limit watch moved the issue to an idle slot on another account.",
			})
			return err
		},
		Send: func(ctx context.Context, t limits.Target, prompt string) error {
			return ph.SendPane(ctx, t.Pane, prompt)
		},
		Escalate: escalateFunc(ctx, root),
		Notify:   func(title, body string) { keepaliveNotify(context.WithoutCancel(ctx), cfg, title, body) },
	}
	rig := &limitRig{Cancel: func() {}}

	var feed *limits.Feed
	if kind == "herdr" {
		ow, ok := h.(host.OutputWatcher)
		if !ok {
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
		if cd, err := roundlease.CommonDir(root); err == nil {
			rec := limits.Watching{PID: os.Getpid(), StartedAt: limits.Time(time.Now()), Mode: limits.ModeSupervisor}
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
	timeout := fs.Float64("timeout", 0, "seconds to watch before stopping; 0 watches until interrupted")
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
		cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
		set, err := limits.LoadSettings(cfg)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error(), Hint: "fix the limits.* key with: rota config set"}
		}
		cd, err := roundlease.CommonDir(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitUnavailable, Message: err.Error()}
		}
		if r, err := limitWatchGuard(c, root, cd); err != nil {
			return r, err
		}

		ctx := c.Context()
		if *timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(*timeout*float64(time.Second)))
			defer cancel()
		}
		tick := time.Duration(*settle * float64(time.Second))
		rig, err := buildLimits(ctx, c, root, cfg, set, hookHolderPID(), tick, nil, func(f string, a ...any) { c.Warn(f, a...) })
		if err != nil {
			return Result{}, err
		}
		defer rig.Cancel()
		rec := limits.Watching{PID: os.Getpid(), StartedAt: limits.Time(time.Now()), Mode: limits.ModeWatch}
		if err := limits.WriteWatching(cd, rec); err != nil {
			c.Warn("watcher record not written: %v", err)
		} else {
			defer limits.RemoveWatching(cd, rec.PID)
		}

		before := limitsJSON(root)
		rig.W.Run(ctx, rig.Feed)
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

// limitWatchGuard refuses a watcher that would act twice or without
// standing: a live supervisor, another watcher, or a caller without the lease.
func limitWatchGuard(c *Ctx, root, cd string) (Result, error) {
	ctx := c.Context()
	le := roundEnv(ctx, root).Lease
	if le.Alive == nil {
		le = roundlease.DefaultEnv()
	}
	lease, st, err := le.Read(cd)
	if err != nil {
		return Result{}, &Error{Exit: ExitUnavailable, Message: err.Error()}
	}
	refuse := func(by, msg, hint string) (Result, error) {
		d := jsonx.NewObject()
		d.Set("blockedBy", by)
		d.Set("changed", false)
		return Result{Data: d}, &Error{Exit: ExitRefused, Message: msg, Hint: hint}
	}
	live := st == roundlease.Live || st == roundlease.Foreign
	if ks, found, _ := keepalive.ReadState(keepalive.StatePath(cd)); live && found && ks.Status == keepalive.StatusRunning && le.Alive(ks.PID) && lease.PID == ks.PID {
		return refuse("supervised", fmt.Sprintf("rota keepalive run (pid %d) holds the lease and already watches for usage limits", ks.PID),
			"run rota limit status, or start the supervisor with --no-limits to watch by hand")
	}
	if !live || !le.Discover(hookHolderPID(), os.Getenv).SameAs(lease, le.Host) {
		return refuse("no round", "this process holds no round lease: run rota round start first", "a switch moves work, which is the orchestrator's act")
	}
	if w, ok := limits.ReadWatching(cd); ok && w.PID != os.Getpid() && le.Alive(w.PID) {
		return refuse("watching", fmt.Sprintf("a usage-limit watcher (%s, pid %d) is already running", w.Mode, w.PID), "one watcher acts on the limits list")
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
	if cd, err := roundlease.CommonDir(root); err == nil {
		le := roundEnv(c.Context(), root).Lease
		if le.Alive == nil {
			le = roundlease.DefaultEnv()
		}
		if w, ok := limits.ReadWatching(cd); ok && le.Alive(w.PID) {
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

func inList(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
