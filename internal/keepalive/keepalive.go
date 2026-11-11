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
	"github.com/l4ci/rota/internal/keepstate"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/tracker"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

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
	keepstate.Handoff
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
	Escalate func(issue tracker.IssueKey, title, body string) (id string, warnings []string, err error)
	// Notify raises the host notification alone.
	Notify func(title, body string)
	// Limits is the usage-limit watcher (D3), run as a goroutine for the
	// life of the supervisor. It returns when ctx ends, with any warnings.
	// Nil means none (`--no-limits`).
	Limits func(ctx context.Context) []string
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
	EscalateIssue tracker.IssueKey

	// Switch is non-nil exactly when switching accounts on a usage handoff
	// is enabled (D4).
	Switch *Switch
}

// Switch is the usage-handoff switch (D4, #206): the account the child starts
// under and how a usage handoff is answered. Every field but Gap must be set.
type Switch struct {
	Threshold int
	// HoldFallback is how long a hold lasts when the marker has no reset time.
	HoldFallback time.Duration
	// ConfigDir is the child's CLAUDE_CONFIG_DIR as inherited and Account its
	// name in round.accounts, empty when it has none.
	ConfigDir string
	Account   string
	// Marker returns the usage handoff written at or after since.
	Marker func(since time.Time) (keepstate.UsageHandoff, bool)
	// Choose picks the account to move to.
	Choose func(currentDir string, threshold int) Choice
	// Record writes the decision to the limits log.
	Record func(Decision) error
	// Gap is true while no child runs, so the embedded limits loop leaves the
	// orchestrator's pane and session alone. Nil: none.
	Gap *atomic.Bool
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

// run is one supervisor run's memory: the pieces Run's steps share.
type run struct {
	env       Env
	o         Options
	res       Result
	st        keepstate.State
	sup       *sup
	statePath string
	warned    bool // the state-file warning was already given
	curDir    string
	stopLim   func()
}

// Run is the supervisor loop. It returns a *roundlease.HeldError when the
// lease belongs to someone else (nothing started), a *SpawnError when the
// command cannot be started, and a plain error for lease or git trouble.
func Run(env Env, o Options) (Result, error) {
	r := &run{env: env.withDefaults(), o: o, sup: &sup{intr: make(chan struct{})}}
	if err := r.acquire(); err != nil {
		return r.res, err
	}
	done := make(chan struct{})
	defer close(done)
	r.forwardSignals(done)
	r.startLimits()

	argv := append([]string{}, o.Command...)
	if o.FirstPrompt != "" {
		argv = append(argv, o.FirstPrompt)
	}
	for {
		runStarted := r.env.Now()
		if reason, cause, stop := r.runChild(argv, runStarted); stop {
			return r.finish(reason, cause)
		}
		if reason, stop := r.decideRestart(runStarted); stop {
			return r.finish(reason, nil)
		}
		r.st.Restarts++
		argv = append(append([]string{}, o.Command...), o.Prompt)
	}
}

// acquire takes the lease and builds the state the run begins with.
func (r *run) acquire() error {
	env, o := r.env, r.o
	_, out, prev, err := env.Lease.Acquire(o.CommonDir, o.Root, env.Holder, 0)
	if err != nil {
		return err
	}
	if out == roundlease.Reclaimed {
		r.res.Reclaimed = prev
		who := "an unreadable lease file"
		if prev.PID > 0 {
			who = fmt.Sprintf("pid %d", prev.PID)
		}
		r.res.Warnings = append(r.res.Warnings, "reclaimed stale lease held by "+who)
	}
	r.statePath = keepstate.StatePath(o.CommonDir)
	start := env.Now()
	r.st = keepstate.State{PID: env.Holder.PID, Host: env.Lease.Host, Start: env.Holder.Start, StartedAt: ts(start), Command: append([]string{}, o.Command...),
		Status: keepstate.StatusRunning, RunStartedAt: ts(start)}
	if o.Switch != nil {
		r.st.Account, r.curDir = o.Switch.Account, o.Switch.ConfigDir
	}
	if h := env.Handoff(); h.Exists {
		r.st.LastHandoffSha = h.SHA // the handoff the first run begins with
	}
	return nil
}

func (r *run) save() {
	if err := keepstate.WriteState(r.statePath, r.st); err != nil && !r.warned {
		r.warned = true
		r.res.Warnings = append(r.res.Warnings, "state file not written: "+err.Error())
	}
}

// forwardSignals hands the supervisor's signals to the child until done closes.
func (r *run) forwardSignals(done <-chan struct{}) {
	if r.env.Signals == nil {
		return
	}
	go func() {
		for {
			select {
			case sig := <-r.env.Signals:
				r.sup.interrupt(sig)
			case <-done:
				return
			}
		}
	}()
}

// startLimits runs the limits loop beside the child; stopLim ends it before
// the lease is released, so it never acts for a round that has no orchestrator.
func (r *run) startLimits() {
	r.stopLim = func() {}
	if r.env.Limits == nil {
		return
	}
	lctx, cancel := context.WithCancel(context.Background())
	ldone := make(chan struct{})
	var lwarns []string
	go func() {
		defer close(ldone)
		lwarns = r.env.Limits(lctx)
	}()
	var once sync.Once
	r.stopLim = func() {
		once.Do(func() {
			cancel()
			<-ldone
			r.res.Warnings = append(r.res.Warnings, lwarns...)
		})
	}
}

func (r *run) finish(reason string, cause error) (Result, error) {
	env, o := r.env, r.o
	r.stopLim()
	r.res.StopReason, r.res.Restarts, r.res.NoProgress, r.res.Switches = reason, r.st.Restarts, r.st.NoProgress, r.st.Switches
	if reason == StopMaxRestarts || reason == StopBreaker {
		r.res.Escalation = escalate(env, o, r.st, reason, r.statePath, &r.res)
		r.st.Escalation = r.res.Escalation
	}
	r.st.Status, r.st.StopReason = keepstate.StatusStopped, reason
	r.save()
	if _, err := env.Lease.Release(o.CommonDir, env.Holder); err != nil {
		r.res.Warnings = append(r.res.Warnings, "lease not released: "+err.Error())
	}
	return r.res, cause
}

func (r *run) setGap(on bool) {
	if r.o.Switch != nil && r.o.Switch.Gap != nil {
		r.o.Switch.Gap.Store(on)
	}
}

// runChild starts argv, waits for it and records how it ended. stop is true
// when the run is over: the child could not run, or a signal arrived.
func (r *run) runChild(argv []string, runStarted time.Time) (reason string, cause error, stop bool) {
	r.st.RunStartedAt = ts(runStarted)
	r.save()
	childEnv := []string{roundlease.HolderPIDEnv + "=" + strconv.Itoa(r.env.Holder.PID)}
	if r.st.Switches > 0 {
		childEnv = append(childEnv, "CLAUDE_CONFIG_DIR="+r.curDir)
	}
	child, err := r.env.Spawn(argv, childEnv)
	if err != nil {
		return StopSpawnFailed, &SpawnError{Err: err}, true
	}
	r.setGap(false)
	r.res.Changed = true
	r.sup.setChild(child)
	exit, werr := child.Wait()
	r.setGap(true)
	r.sup.setChild(nil)
	if werr != nil {
		return StopSpawnFailed, &SpawnError{Err: werr}, true
	}
	r.res.LastExit = exit
	r.st.LastExit = &keepstate.LastExit{Code: exit.Code, Signal: exit.Signal, At: ts(r.env.Now())}
	r.save()
	if r.sup.wasInterrupted() {
		return StopInterrupted, nil, true
	}
	return "", nil, false
}

// decideRestart judges the handoff the child left and, when the run goes on,
// waits out the backoff and applies a usage handoff.
func (r *run) decideRestart(runStarted time.Time) (reason string, stop bool) {
	env, o, st := r.env, r.o, &r.st
	ho := env.Handoff()
	if !ho.Fresh(o.HandoffMaxAge, env.Now()) {
		return StopNoHandoff, true
	}
	if !ho.ModTime.Before(runStarted) && ho.SHA != st.LastHandoffSha {
		st.NoProgress, st.LastHandoffSha = 0, ho.SHA
	} else {
		st.NoProgress++
	}
	if st.Restarts >= o.MaxRestarts {
		return StopMaxRestarts, true
	}
	if st.NoProgress >= o.Breaker {
		return StopBreaker, true
	}
	r.save()
	if !env.Sleep(o.Backoff, r.sup.intr) || r.sup.wasInterrupted() {
		return StopInterrupted, true
	}
	if sw := o.Switch; sw != nil {
		if m, ok := sw.Marker(runStarted); ok {
			decideUsage(env, sw, st, &r.curDir, m, func(w string) { r.res.Warnings = append(r.res.Warnings, w) })
		}
	}
	return "", false
}

func ts(t time.Time) string { return stamp.Format(t) }

// escalate raises the breaker's notification and returns the escalation id,
// "" when none was recorded. Failures are warnings: the stop stands.
func escalate(env Env, o Options, st keepstate.State, reason, statePath string, res *Result) string {
	title := "Orchestrator keepalive stopped: " + reason
	body := escalationBody(env, o, st, reason, statePath)
	if o.EscalateIssue == "" {
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
		res.Warnings = append(res.Warnings, fmt.Sprintf("escalation on %s not sent: %v", o.EscalateIssue.Ref(), err))
		return ""
	}
	return id
}

func escalationBody(env Env, o Options, st keepstate.State, reason, statePath string) string {
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
