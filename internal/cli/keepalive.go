package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/keepstate"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/keepalive"
	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

// D2 (#66): `rota keepalive run|status`. The loop is internal/keepalive; this
// file is the process side: the child, signals, the lease environment, the
// handoff file and the escalation entry.

func keepaliveCommands() *Command {
	return &Command{Name: "keepalive", Summary: "restart the orchestrator in its pane when it exits with a fresh handoff", Subs: []*Command{
		{Name: "run", Fence: fenceAllowed, Summary: "run a command as a supervisor: rota keepalive run [flags] -- <command> [<arg>...]", Verb: keepaliveRun},
		{Name: "status", Fence: fenceAllowed, Summary: "show the supervisor state and the round lease", Verb: noFlags(keepaliveStatus)},
	}}
}

// procChild is a process run through proc.Runner in the background: proc
// hands over the started process, Signal sends to it, Wait collects how it
// ended. Signalling the *os.Process, not a pid, means a signal arriving after
// proc reaped the child gets os.ErrProcessDone instead of a reused pid.
type procChild struct {
	proc *os.Process
	done chan procExit
}

type procExit struct {
	res proc.Result
	err error
}

func (c *procChild) Signal(s os.Signal) error {
	if c.proc == nil {
		return os.ErrProcessDone
	}
	return c.proc.Signal(s)
}

func (c *procChild) Wait() (keepalive.Exit, error) {
	x := <-c.done
	if x.err != nil {
		return keepalive.Exit{}, x.err
	}
	if x.res.Signal != 0 {
		sig := syscall.Signal(x.res.Signal)
		return keepalive.Exit{Code: 128 + x.res.Signal, Signal: signalName(sig)}, nil
	}
	return keepalive.Exit{Code: x.res.ExitCode}, nil
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
// A configDir, when set, is the child's CLAUDE_CONFIG_DIR from the first start.
func keepaliveSpawn(c *Ctx, configDir string) func(argv, extraEnv []string) (keepalive.Child, error) {
	return func(argv, extraEnv []string) (keepalive.Child, error) {
		// Inherited stdio and no timeout on purpose: the child owns the
		// terminal for its whole life, in the supervisor's own process group.
		env := append([]string{}, c.deps().Environ()...)
		if configDir != "" {
			env = append(env, "CLAUDE_CONFIG_DIR="+configDir)
		}
		env = append(env, extraEnv...)
		stdio := &proc.Stdio{Out: c.Stdout, Err: c.Stderr}
		if f, ok := c.Stdin.(*os.File); ok {
			stdio.In = f
		}
		started := make(chan *os.Process, 1)
		child := &procChild{done: make(chan procExit, 1)}
		go func() {
			r, err := c.deps().Proc(context.Background(), proc.Cmd{
				Name: argv[0], Args: argv[1:], Environ: env, Timeout: proc.NoTimeout,
				Stdio: stdio, OnStart: func(p *os.Process) { started <- p },
			})
			child.done <- procExit{r, err}
			close(started)
		}()
		p, ok := <-started
		if !ok { // the run ended without reporting a pid: it never started, or a fake ran it
			x := <-child.done
			if x.err != nil {
				return nil, x.err
			}
			child.done <- x
			return child, nil
		}
		child.proc = p
		return child, nil
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
	maxRestarts := fs.String("max-restarts", "", "restarts before giving up (default 10)")
	breaker := fs.String("breaker", "", "restarts without a new handoff before the breaker trips (default 3)")
	backoff := fs.String("backoff", "", "seconds to wait before a restart (default 5)")
	noLimits := fs.Bool("no-limits", false, "do not run the usage-limit watcher (rota limit watch) beside the command")
	prompt := fs.String("prompt", "", "restart prompt, appended as the last argument on restarts (default orchestrator.restartPrompt)")
	first := fs.String("first-prompt", "", "prompt appended as the last argument of the first start only (default none)")
	configDir := fs.String("config-dir", "", "CLAUDE_CONFIG_DIR the command starts under (default the inherited one)")
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
		cfg := c.deps().Config(root)
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
		cd, err := c.CommonDir(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitUnavailable, Message: err.Error()}
		}
		ctx := c.Context()
		le := c.deps().LeaseEnv()

		sigs := make(chan os.Signal, 8)
		signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigs)

		handoffPath := handoffFile(c.deps(), root, cfg)
		if handoffPath == "" {
			c.Warn("%s: the orchestrator handoff is not watched", noHandoffBaseMsg(cfg))
		}
		env := keepalive.Env{
			Spawn:    keepaliveSpawn(c, *configDir),
			Signals:  sigs,
			Lease:    le,
			Holder:   le.Discover(c.deps().PID(), c.deps().Getenv),
			Handoff:  func() keepalive.HandoffRead { return readHandoff(handoffPath) },
			Escalate: escalateFunc(ctx, c, root),
			Notify:   func(title, body string) { keepaliveNotify(context.WithoutCancel(ctx), c, root, title, body) },
		}
		opts := keepalive.Options{
			Command: args, Root: root, CommonDir: cd, HandoffPath: handoffPath,
			HandoffMaxAge: set.HandoffMaxAge, MaxRestarts: set.MaxRestarts, Breaker: set.Breaker,
			Backoff: set.Backoff, Prompt: set.Prompt, FirstPrompt: *first, EscalateIssue: set.EscalateIssue,
		}
		var gap *atomic.Bool
		if set.SwitchOnUsage {
			gap = new(atomic.Bool)
			dir := c.deps().Getenv("CLAUDE_CONFIG_DIR")
			if *configDir != "" {
				dir = *configDir
			}
			opts.Switch = &keepalive.Switch{
				Threshold: set.UsageThreshold, HoldFallback: set.HoldFallback,
				ConfigDir: dir, Account: c.deps().WorkerAccounts().AccountOf(root, dir),
				Marker: hook.UsageMarker(cd, root), Choose: usageChoose(ctx, c, root),
				Record: keepalive.UsageRecorder(root, c.deps().Now), Gap: gap,
			}
		}
		if !*noLimits {
			lset, err := limits.LoadSettings(cfg)
			if err != nil {
				return Result{}, &Error{Exit: ExitInternal, Message: err.Error(), Hint: "fix the limits.* key with: rota config set"}
			}
			env.Limits = limitsLoop(c, root, cfg, lset, c.deps().PID(), gap)
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
				d.Set("blockedBy", blockedby.LeaseHeld)
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

// usageChoose asks the account meters where an orchestrator at its threshold
// can go (D4).
func usageChoose(ctx context.Context, c *Ctx, root string) func(string, int) keepalive.Choice {
	return func(currentDir string, threshold int) keepalive.Choice {
		m, ok, others := c.deps().WorkerAccounts().OrchestratorTarget(ctx, root, currentDir, threshold)
		if !ok {
			return keepalive.Choice{Others: others}
		}
		return keepalive.Choice{
			To:     &keepalive.Target{Name: m.Name, ConfigDir: worker.ExpandConfigDirIn(m.ConfigDir, homeOf(c.deps().UserHomeDir)), Headroom: *m.Headroom},
			Others: others,
		}
	}
}

// keepaliveNotify raises the round host's notification alone, when that host
// has a notification side.
func keepaliveNotify(ctx context.Context, c *Ctx, root string, title, body string) {
	kind := c.deps().resolveHost(root)
	if kind == host.Solo {
		return
	}
	h := c.deps().Host(kind)
	n := h.Capabilities().Notifier
	if n == nil || h.Require() != nil {
		return
	}
	n.Notify(ctx, title, body)
}

func keepaliveStatus(c *Ctx, args []string) (Result, error) {
	if err := noArgs(args); err != nil {
		return Result{}, err
	}
	root, err := c.Root()
	if err != nil {
		return Result{}, err
	}
	cd, err := c.CommonDir(root)
	if err != nil {
		return Result{}, &Error{Exit: ExitUnavailable, Message: err.Error()}
	}
	le := c.deps().LeaseEnv()
	l, st, err := le.Read(cd)
	if err != nil {
		return Result{}, err
	}
	ks, found, err := keepstate.ReadState(keepstate.StatePath(cd))
	if err != nil {
		return Result{}, &Error{Exit: ExitUnavailable, Message: err.Error()}
	}
	running := found && ks.Running(le)

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
		raw, _ := os.ReadFile(keepstate.StatePath(cd))
		if v, derr := jsonx.Decode(raw); derr == nil {
			d.Set("keepalive", v)
		}
	}
	return Result{Data: d, Text: text}, nil
}

func durSeconds(n int) time.Duration { return time.Duration(n) * time.Second }
