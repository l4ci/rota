package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/keepalive"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

// D2 (#66): `rota keepalive run|status`. The loop is internal/keepalive; this
// file is the process side: the child, signals, the lease environment, the
// handoff file and the escalation entry.

func keepaliveCommands() *Command {
	return &Command{Name: "keepalive", Summary: "restart the orchestrator in its pane when it exits with a fresh handoff", Subs: []*Command{
		{Name: "run", Summary: "run a command as a supervisor: rota keepalive run [flags] -- <command> [<arg>...]", Verb: keepaliveRun},
		{Name: "status", Summary: "show the supervisor state and the round lease", Verb: noFlags(keepaliveStatus)},
	}}
}

// osChild is a started process.
type osChild struct{ cmd *exec.Cmd }

func (c *osChild) Signal(s os.Signal) error { return c.cmd.Process.Signal(s) }

func (c *osChild) Wait() (keepalive.Exit, error) {
	err := c.cmd.Wait()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return keepalive.Exit{}, err
	}
	ps := c.cmd.ProcessState
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return keepalive.Exit{Code: 128 + int(ws.Signal()), Signal: signalName(ws.Signal())}, nil
	}
	return keepalive.Exit{Code: ps.ExitCode()}, nil
}

func signalName(s syscall.Signal) string {
	switch s {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	}
	return "SIG" + strconv.Itoa(int(s))
}

// keepaliveSpawn starts the command in the current directory with the
// terminal's stdio and the supervisor's process group.
func keepaliveSpawn(c *Ctx) func(argv, extraEnv []string) (keepalive.Child, error) {
	return func(argv, extraEnv []string) (keepalive.Child, error) {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Env = append(os.Environ(), extraEnv...)
		if f, ok := c.Stdin.(*os.File); ok {
			cmd.Stdin = f
		}
		cmd.Stdout, cmd.Stderr = c.Stdout, c.Stderr
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &osChild{cmd: cmd}, nil
	}
}

// readHandoff stats the handoff file and hashes it.
func readHandoff(path string) keepalive.HandoffRead {
	h := keepalive.HandoffRead{Handoff: hook.StatHandoff(path)}
	if h.Exists {
		if b, err := os.ReadFile(path); err == nil {
			sum := sha256.Sum256(b)
			h.SHA = hex.EncodeToString(sum[:])
		}
	}
	return h
}

// keepaliveInt parses a number flag; "" means not given.
func keepaliveInt(name, val string, min int) (n int, given bool, err error) {
	if val == "" {
		return 0, false, nil
	}
	n, perr := strconv.Atoi(val)
	if perr != nil || n < min || n > 1<<30 {
		return 0, false, Usage("--%s must be an integer of at least %d (got %q)", name, min, val)
	}
	return n, true, nil
}

func keepaliveRun(fs *flag.FlagSet) RunFunc {
	maxRestarts := fs.String("max-restarts", "", "restarts before giving up (default orchestrator.keepaliveMaxRestarts)")
	breaker := fs.String("breaker", "", "restarts without a new handoff before the breaker trips (default orchestrator.keepaliveBreaker)")
	backoff := fs.String("backoff", "", "seconds to wait before a restart (default orchestrator.keepaliveBackoffSeconds)")
	noLimits := fs.Bool("no-limits", false, "do not run the usage-limit watcher (rota limit watch) beside the command")
	prompt := fs.String("prompt", "", "restart prompt, appended as the last argument on restarts (default orchestrator.restartPrompt)")
	first := fs.String("first-prompt", "", "prompt appended as the last argument of the first start only (default none)")
	return func(c *Ctx, args []string) (Result, error) {
		if c.dashAt != 0 {
			return Result{}, Usage("usage: rota keepalive run [flags] -- <command> [<arg>...]").
				WithHint("the command goes after --, e.g. rota keepalive run -- claude")
		}
		if len(args) == 0 {
			return Result{}, Usage("missing <command> after --")
		}
		mr, mrSet, err := keepaliveInt("max-restarts", *maxRestarts, 0)
		if err != nil {
			return Result{}, err
		}
		br, brSet, err := keepaliveInt("breaker", *breaker, 1)
		if err != nil {
			return Result{}, err
		}
		bo, boSet, err := keepaliveInt("backoff", *backoff, 0)
		if err != nil {
			return Result{}, err
		}
		promptSet := false
		fs.Visit(func(f *flag.Flag) { promptSet = promptSet || f.Name == "prompt" })
		if promptSet && strings.TrimSpace(*prompt) == "" {
			return Result{}, Usage("--prompt must not be empty")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		cfg := config.Load(filepath.Join(root, ".rota", "config.json"))
		set, err := keepalive.LoadSettings(cfg)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error(), Hint: "fix the orchestrator.* key with: rota config set"}
		}
		if mrSet {
			set.MaxRestarts = mr
		}
		if brSet {
			set.Breaker = br
		}
		if boSet {
			set.Backoff = durSeconds(bo)
		}
		if promptSet {
			set.Prompt = *prompt
		}
		cd, err := rotastate.CommonDir(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitUnavailable, Message: err.Error()}
		}
		ctx := c.Context()
		le := c.deps().RoundEnv(ctx, root).Lease
		if le.Alive == nil {
			le = roundlease.DefaultEnv()
		}

		sigs := make(chan os.Signal, 8)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigs)

		env := keepalive.Env{
			Spawn:    keepaliveSpawn(c),
			Signals:  sigs,
			Lease:    le,
			Holder:   le.Discover(os.Getpid(), os.Getenv),
			Handoff:  func() keepalive.HandoffRead { return readHandoff(handoffFile(root, cfg)) },
			Escalate: escalateFunc(ctx, c, root),
			Notify:   func(title, body string) { keepaliveNotify(context.WithoutCancel(ctx), c, root, title, body) },
		}
		gap := keepaliveGap(set.SwitchOnUsage)
		env.Gap = gap
		opts := keepalive.Options{
			Command: args, Root: root, CommonDir: cd, HandoffPath: handoffFile(root, cfg),
			HandoffMaxAge: set.HandoffMaxAge, MaxRestarts: set.MaxRestarts, Breaker: set.Breaker,
			Backoff: set.Backoff, Prompt: set.Prompt, FirstPrompt: *first, EscalateIssue: set.EscalateIssue,
		}
		if set.SwitchOnUsage {
			opts.SwitchOnUsage, opts.UsageThreshold, opts.HoldFallback = true, set.UsageThreshold, set.HoldFallback
			opts.ConfigDir = os.Getenv("CLAUDE_CONFIG_DIR")
			opts.Account = worker.AccountOf(root, opts.ConfigDir)
			env.UsageMarker = usageMarker(cd, root)
			env.Choose = usageChoose(ctx, c, root)
			env.Record = usageRecord(root, hookNow)
		}
		if !*noLimits {
			lset, err := limits.LoadSettings(cfg)
			if err != nil {
				return Result{}, &Error{Exit: ExitInternal, Message: err.Error(), Hint: "fix the limits.* key with: rota config set"}
			}
			env.Limits = limitsLoop(c, root, cfg, lset, os.Getpid(), gap)
		}
		res, err := keepalive.Run(env, opts)
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		if err != nil {
			var held *roundlease.HeldError
			var spawn *keepalive.SpawnError
			switch {
			case errors.As(err, &held):
				d := jsonx.NewObject()
				d.Set("blockedBy", "lease held")
				d.Set("lease", leaseData(held.Lease, held.State))
				d.Set("changed", false)
				return Result{Data: d}, &Error{Exit: ExitRefused, Message: held.Error(),
					Hint: "stop that orchestrator first, or run: rota keepalive status"}
			case errors.As(err, &spawn):
				return Result{}, &Error{Exit: ExitUnavailable, Message: fmt.Sprintf("cannot run %s: %v", args[0], spawn.Err)}
			}
			return Result{}, &Error{Exit: ExitUnavailable, Message: err.Error()}
		}
		le2 := jsonx.NewObject()
		le2.Set("code", res.LastExit.Code)
		setIf(le2, "signal", res.LastExit.Signal)
		d := jsonx.NewObject()
		d.Set("stopReason", res.StopReason)
		d.Set("restarts", res.Restarts)
		d.Set("noProgress", res.NoProgress)
		d.Set("switches", res.Switches)
		d.Set("lastExit", le2)
		setIf(d, "escalation", res.Escalation)
		d.Set("changed", res.Changed)
		text := fmt.Sprintf("keepalive stopped: %s (restarts %d, no progress %d)", res.StopReason, res.Restarts, res.NoProgress)
		if res.StopReason == keepalive.StopMaxRestarts || res.StopReason == keepalive.StopBreaker {
			return Result{Data: d, Text: text}, Failed("keepalive stopped: %s", res.StopReason)
		}
		return Result{Data: d, Text: text}, nil
	}
}

// keepaliveGap is the flag the supervisor sets between children, nil unless
// the switch is on: with it off, D3's loop must not see a gap at all.
func keepaliveGap(switchOnUsage bool) *atomic.Bool {
	if !switchOnUsage {
		return nil
	}
	return new(atomic.Bool)
}

// usageMarker finds the newest usage handoff written at or after since in the
// session files whose cwd is the project root (D4).
func usageMarker(commonDir, root string) func(since time.Time) (hook.UsageHandoff, bool) {
	return func(since time.Time) (hook.UsageHandoff, bool) {
		var best hook.UsageHandoff
		var bestAt time.Time
		for _, st := range hook.Sessions(commonDir) {
			if st.UsageHandoff == nil || !sameDir(st.Cwd, root) {
				continue
			}
			at, err := time.Parse(time.RFC3339, st.UsageHandoff.At)
			if err != nil || at.Before(since.Truncate(time.Second)) {
				continue
			}
			if bestAt.IsZero() || at.After(bestAt) {
				best, bestAt = *st.UsageHandoff, at
			}
		}
		return best, !bestAt.IsZero()
	}
}

// usageChoose asks the account meters where an orchestrator at its threshold
// can go (D4).
func usageChoose(ctx context.Context, c *Ctx, root string) func(string, int) keepalive.Choice {
	return func(currentDir string, threshold int) keepalive.Choice {
		m, ok, others := c.deps().WorkerAccounts().OrchestratorTarget(ctx, root, currentDir, threshold)
		if !ok {
			return keepalive.Choice{Others: others}
		}
		return keepalive.Choice{
			To:     &keepalive.Target{Name: m.Name, ConfigDir: worker.ExpandConfigDir(m.ConfigDir), Headroom: *m.Headroom},
			Others: others,
		}
	}
}

// usageRecord writes a usage decision to the limits log in .rota/workers.json.
func usageRecord(root string, now func() time.Time) func(keepalive.Decision) error {
	return func(d keepalive.Decision) error {
		status := limits.StatusSwitched
		if d.Action == keepalive.ActionRestart {
			status = limits.StatusResumed
		}
		t := limits.Time(now())
		_, err := limits.Append(root, limits.Entry{
			Session: limits.Orchestrator, Window: d.Marker.Window, Source: limits.SourceData,
			DetectedAt: d.Marker.At, ResetsAt: d.Marker.ResetsAt, Action: d.Action,
			Account: d.From, Status: status, ResolvedAt: t, Note: d.Note,
		})
		return err
	}
}

// keepaliveNotify raises the herdr notification alone, when the round's host
// is herdr.
func keepaliveNotify(ctx context.Context, c *Ctx, root string, title, body string) {
	if worker.ResolveHost(root, nil, nil) != "herdr" {
		return
	}
	var h host.Host
	if ee := c.deps().EscalationEnv(); ee.Host != nil {
		h = ee.Host()
	} else {
		h = host.New("herdr", host.Deps{})
	}
	if h.Require() != nil {
		return
	}
	h.Notify(ctx, title, body)
}

func keepaliveStatus(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	ctx := c.Context()
	env := c.deps().RoundEnv(ctx, root)
	l, st, err := env.ReadLease(ctx, root)
	if err != nil {
		return Result{}, err
	}
	cd, err := rotastate.CommonDir(root)
	if err != nil {
		return Result{}, &Error{Exit: ExitUnavailable, Message: err.Error()}
	}
	ks, found, err := keepalive.ReadState(keepalive.StatePath(cd))
	if err != nil {
		return Result{}, &Error{Exit: ExitUnavailable, Message: err.Error()}
	}
	le := env.Lease
	if le.Alive == nil {
		le = roundlease.DefaultEnv()
	}
	running := found && ks.Status == keepalive.StatusRunning && le.Alive(ks.PID)

	lo := jsonx.NewObject()
	lo.Set("state", string(st))
	if st != roundlease.None && l.PID > 0 {
		lo.Set("holderPid", l.PID)
		lo.Set("round", l.Round)
	}
	d := jsonx.NewObject()
	d.Set("running", running)
	d.Set("lease", lo)
	text := "keepalive: not running"
	if running {
		text = fmt.Sprintf("keepalive: running, pid %d, restarts %d, no progress %d", ks.PID, ks.Restarts, ks.NoProgress)
	} else if found {
		text = fmt.Sprintf("keepalive: %s", ks.Status)
		if ks.StopReason != "" {
			text += " (" + ks.StopReason + ")"
		}
	}
	text += fmt.Sprintf("\nlease: %s", st)
	if st != roundlease.None && l.PID > 0 {
		text += fmt.Sprintf(", holder pid %d, round %d", l.PID, l.Round)
	}
	if found {
		raw, _ := os.ReadFile(keepalive.StatePath(cd))
		if v, derr := jsonx.Decode(raw); derr == nil {
			d.Set("keepalive", v)
		}
	}
	return Result{Data: d, Text: text}, nil
}

func durSeconds(n int) time.Duration { return time.Duration(n) * time.Second }
