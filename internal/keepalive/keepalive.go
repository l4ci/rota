// Package keepalive is the logic behind `rota keepalive run` (D2, #66): a
// supervisor that runs the orchestrator as its child, holds the round lease
// for its whole life, and restarts the child when it exited leaving a fresh
// handoff.
//
// Everything outside its own memory is injected (spawn, clock, sleep,
// signals, the lease environment, the handoff reader, escalation and
// notification), so tests need no real process, forge or Claude.
package keepalive

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/roundlease"
)

// Stop reasons of Result.StopReason.
const (
	StopNoHandoff   = "no-handoff"
	StopInterrupted = "interrupted"
	StopMaxRestarts = "max-restarts"
	StopBreaker     = "breaker"
	// StopSpawnFailed appears in the state file only: the verb exits 5.
	StopSpawnFailed = "spawn-failed"
)

// Exit is how a child ended. Signal is set when it was killed by one.
type Exit struct {
	Code   int
	Signal string
}

// Child is a started process.
type Child interface {
	Signal(os.Signal) error
	Wait() (Exit, error)
}

// HandoffRead is what the filesystem says about the handoff file, with the
// SHA-256 of its content (empty when it does not exist).
type HandoffRead struct {
	hook.Handoff
	SHA string
}

// Env is everything Run touches outside its own memory.
type Env struct {
	// Spawn starts argv with extraEnv added to the inherited environment.
	Spawn func(argv, extraEnv []string) (Child, error)
	Now   func() time.Time
	// Sleep waits d and returns false when intr closed first.
	Sleep func(d time.Duration, intr <-chan struct{}) bool
	// Signals delivers SIGINT and SIGTERM sent to the supervisor.
	Signals <-chan os.Signal
	Lease   roundlease.Env
	Holder  roundlease.Holder
	// Handoff reads the handoff file now.
	Handoff func() HandoffRead
	// Escalate posts the stop on the issue thread and returns the
	// escalation id and any warnings.
	Escalate func(issue int, title, body string) (id string, warnings []string, err error)
	// Notify raises the host notification alone.
	Notify func(title, body string)
	// Limits is the usage-limit watcher (D3), run as a goroutine for the
	// life of the supervisor. It returns when ctx ends, with any warnings.
	// Nil means none (`--no-limits`).
	Limits func(ctx context.Context) []string

	// D4 (#206), used only when Options.SwitchOnUsage is set. UsageMarker
	// returns the usage handoff written at or after since; Choose picks the
	// account to move to; Record writes the decision to the limits log. Gap is
	// true while no child runs, so the embedded loop leaves the orchestrator's
	// pane and session alone.
	UsageMarker func(since time.Time) (hook.UsageHandoff, bool)
	Choose      func(currentDir string, threshold int) Choice
	Record      func(Decision) error
	Gap         *atomic.Bool
}

// Options are one run's inputs, flags already merged over config.
type Options struct {
	Command       []string
	Root          string
	CommonDir     string
	HandoffPath   string
	HandoffMaxAge time.Duration
	MaxRestarts   int
	Breaker       int
	Backoff       time.Duration
	Prompt        string
	// FirstPrompt is appended to Command on the first start only, where
	// Prompt is appended on restarts. Empty: the first start is Command alone.
	FirstPrompt   string
	EscalateIssue int

	// D4: the switch and the account the child starts under. ConfigDir is the
	// child's CLAUDE_CONFIG_DIR as inherited and Account its name in
	// work.accounts, empty when it has none. HoldFallback is how long a hold
	// lasts when the marker has no reset time.
	SwitchOnUsage  bool
	UsageThreshold int
	HoldFallback   time.Duration
	ConfigDir      string
	Account        string
}

// Result is what Run did. On an error it holds whatever happened before it.
type Result struct {
	StopReason string
	Restarts   int
	NoProgress int
	Switches   int
	LastExit   Exit
	Escalation string
	Warnings   []string
	// Changed is true once a child was started.
	Changed bool
	// Reclaimed is the stale lease the run replaced, zero when none.
	Reclaimed roundlease.Lease
}

// SpawnError is a failure to start or wait for the command (exit 5).
type SpawnError struct{ Err error }

func (e *SpawnError) Error() string { return e.Err.Error() }
func (e *SpawnError) Unwrap() error { return e.Err }

func (e Env) withDefaults() Env {
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Sleep == nil {
		e.Sleep = realSleep
	}
	return e
}

func realSleep(d time.Duration, intr <-chan struct{}) bool {
	if d <= 0 {
		select {
		case <-intr:
			return false
		default:
			return true
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-intr:
		return false
	}
}

// sup forwards signals to the current child and remembers that one came.
type sup struct {
	mu          sync.Mutex
	child       Child
	interrupted bool
	sig         os.Signal
	intr        chan struct{}
}

func (s *sup) interrupt(sig os.Signal) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.interrupted {
		s.interrupted = true
		close(s.intr)
	}
	s.sig = sig
	if s.child != nil {
		_ = s.child.Signal(sig)
	}
}

// setChild records the running child. A signal that arrived before it was
// recorded is forwarded now.
func (s *sup) setChild(c Child) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.child = c
	if c != nil && s.interrupted && s.sig != nil {
		_ = c.Signal(s.sig)
	}
}

func (s *sup) wasInterrupted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interrupted
}

// Run is the supervisor loop. It returns a *roundlease.HeldError when the
// lease belongs to someone else (nothing started), a *SpawnError when the
// command cannot be started, and a plain error for lease or git trouble.
func Run(env Env, o Options) (Result, error) {
	env = env.withDefaults()
	var res Result
	_, out, prev, err := env.Lease.Acquire(o.CommonDir, o.Root, env.Holder, 0)
	if err != nil {
		return res, err
	}
	if out == roundlease.Reclaimed {
		res.Reclaimed = prev
		who := "an unreadable lease file"
		if prev.PID > 0 {
			who = fmt.Sprintf("pid %d", prev.PID)
		}
		res.Warnings = append(res.Warnings, "reclaimed stale lease held by "+who)
	}

	statePath := StatePath(o.CommonDir)
	start := env.Now()
	st := State{PID: env.Holder.PID, Host: env.Lease.Host, Start: env.Holder.Start, StartedAt: ts(start), Command: append([]string{}, o.Command...),
		Status: StatusRunning, RunStartedAt: ts(start), Account: o.Account}
	if h := env.Handoff(); h.Exists {
		st.LastHandoffSha = h.SHA // the handoff the first run begins with
	}
	stateWarned := false
	save := func() {
		if err := WriteState(statePath, st); err != nil && !stateWarned {
			stateWarned = true
			res.Warnings = append(res.Warnings, "state file not written: "+err.Error())
		}
	}

	s := &sup{intr: make(chan struct{})}
	done := make(chan struct{})
	defer close(done)
	if env.Signals != nil {
		go func() {
			for {
				select {
				case sig := <-env.Signals:
					s.interrupt(sig)
				case <-done:
					return
				}
			}
		}()
	}

	// The limits loop runs beside the child and ends before the lease is
	// released, so it never acts for a round that has no orchestrator.
	stopLimits := func() {}
	if env.Limits != nil {
		lctx, cancel := context.WithCancel(context.Background())
		ldone := make(chan struct{})
		var lwarns []string
		go func() {
			defer close(ldone)
			lwarns = env.Limits(lctx)
		}()
		var once sync.Once
		stopLimits = func() {
			once.Do(func() {
				cancel()
				<-ldone
				res.Warnings = append(res.Warnings, lwarns...)
			})
		}
	}

	finish := func(reason string, cause error) (Result, error) {
		stopLimits()
		res.StopReason, res.Restarts, res.NoProgress, res.Switches = reason, st.Restarts, st.NoProgress, st.Switches
		if reason == StopMaxRestarts || reason == StopBreaker {
			res.Escalation = escalate(env, o, st, reason, statePath, &res)
			st.Escalation = res.Escalation
		}
		st.Status, st.StopReason = StatusStopped, reason
		save()
		if _, err := env.Lease.Release(o.CommonDir, env.Holder); err != nil {
			res.Warnings = append(res.Warnings, "lease not released: "+err.Error())
		}
		return res, cause
	}

	argv := append([]string{}, o.Command...)
	if o.FirstPrompt != "" {
		argv = append(argv, o.FirstPrompt)
	}
	pidEnv := []string{roundlease.HolderPIDEnv + "=" + strconv.Itoa(env.Holder.PID)}
	setGap := func(on bool) {
		if env.Gap != nil {
			env.Gap.Store(on)
		}
	}
	curDir := o.ConfigDir
	for {
		runStarted := env.Now()
		st.RunStartedAt = ts(runStarted)
		save()
		childEnv := pidEnv
		if st.Switches > 0 {
			childEnv = append(append([]string{}, pidEnv...), "CLAUDE_CONFIG_DIR="+curDir)
		}
		child, err := env.Spawn(argv, childEnv)
		if err != nil {
			return finish(StopSpawnFailed, &SpawnError{Err: err})
		}
		setGap(false)
		res.Changed = true
		s.setChild(child)
		exit, werr := child.Wait()
		setGap(true)
		s.setChild(nil)
		if werr != nil {
			return finish(StopSpawnFailed, &SpawnError{Err: werr})
		}
		res.LastExit = exit
		st.LastExit = &LastExit{Code: exit.Code, Signal: exit.Signal, At: ts(env.Now())}
		save()

		if s.wasInterrupted() {
			return finish(StopInterrupted, nil)
		}
		ho := env.Handoff()
		if !ho.Fresh(o.HandoffMaxAge, env.Now()) {
			return finish(StopNoHandoff, nil)
		}
		if !ho.ModTime.Before(runStarted) && ho.SHA != st.LastHandoffSha {
			st.NoProgress, st.LastHandoffSha = 0, ho.SHA
		} else {
			st.NoProgress++
		}
		if st.Restarts >= o.MaxRestarts {
			return finish(StopMaxRestarts, nil)
		}
		if st.NoProgress >= o.Breaker {
			return finish(StopBreaker, nil)
		}
		save()
		if !env.Sleep(o.Backoff, s.intr) || s.wasInterrupted() {
			return finish(StopInterrupted, nil)
		}
		if o.SwitchOnUsage && env.UsageMarker != nil && env.Choose != nil {
			if m, ok := env.UsageMarker(runStarted); ok {
				decideUsage(env, o, &st, &curDir, m, func(w string) { res.Warnings = append(res.Warnings, w) })
			}
		}
		st.Restarts++
		argv = append(append([]string{}, o.Command...), o.Prompt)
	}
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// escalate raises the breaker's notification and returns the escalation id,
// "" when none was recorded. Failures are warnings: the stop stands.
func escalate(env Env, o Options, st State, reason, statePath string, res *Result) string {
	title := "Orchestrator keepalive stopped: " + reason
	body := escalationBody(env, o, st, reason, statePath)
	if o.EscalateIssue == 0 {
		if env.Notify != nil {
			env.Notify(title, fmt.Sprintf("restarts %d, no progress %d", st.Restarts, st.NoProgress))
		}
		res.Warnings = append(res.Warnings, "no escalation comment: orchestrator.escalateIssue is unset, so only a host notification was raised")
		return ""
	}
	if env.Escalate == nil {
		res.Warnings = append(res.Warnings, "no escalation comment: no escalation channel")
		return ""
	}
	id, warns, err := env.Escalate(o.EscalateIssue, title, body)
	res.Warnings = append(res.Warnings, warns...)
	if err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("escalation on #%d not sent: %v", o.EscalateIssue, err))
		return ""
	}
	return id
}

func escalationBody(env Env, o Options, st State, reason, statePath string) string {
	last := "none"
	if st.LastExit != nil {
		last = fmt.Sprintf("code %d", st.LastExit.Code)
		if st.LastExit.Signal != "" {
			last += ", signal " + st.LastExit.Signal
		}
		last += " at " + st.LastExit.At
	}
	handoff := o.HandoffPath + " (missing)"
	if h := env.Handoff(); h.Exists {
		handoff = fmt.Sprintf("%s (age %ds)", o.HandoffPath, int(env.Now().Sub(h.ModTime).Seconds()))
	}
	return fmt.Sprintf("The orchestrator keepalive supervisor stopped: %s.\n\n"+
		"- Restarts: %d\n- Restarts that left no new handoff: %d\n- Last exit: %s\n- Handoff: %s\n- State file: %s\n\n"+
		"Run rota keepalive run again after fixing the cause; the handoff is kept.\n",
		reason, st.Restarts, st.NoProgress, last, handoff, statePath)
}
