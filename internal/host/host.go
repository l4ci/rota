// Package host drives the terminal host that runs /rota-work worker sessions:
// tmux windows, legacy herdr tabs, Core terminals or Orca terminals. Orca is the opt-in adapter.
//
// SAFETY: closing a herdr tab, tmux window or Orca terminal kills live agents.
// Nothing here execs a binary directly:
// every command goes through Deps.Run, which tests replace with a fake. The
// default runner (ExecRunner) is the only place that reaches os/exec.
package host

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/proc"
)

// Solo is the round host of a round with no terminal: workers are in-harness
// subagents, so there is no pane to drive. It is a round-level value only:
// New never returns it, and no Host is built for it.
const Solo Kind = "solo"

// Kind names a host backend. It is a string on the wire (the registry, the
// round report); the constants below are its only spellings.
type Kind string

// The host kinds that have panes.
const (
	Herdr     Kind = "herdr"
	HerdrCore Kind = "herdr-core"
	Tmux      Kind = "tmux"
	Orca      Kind = "orca"
)

// KindOf is the kind a config or registry string names: "herdr", "herdr-core", "tmux", "orca" and "solo",
// anything else (including "subagent" and "") the zero Kind.
func KindOf(s string) Kind {
	switch k := Kind(s); k {
	case Herdr, HerdrCore, Tmux, Orca, Solo:
		return k
	}
	return ""
}

// String is the kind's wire spelling.
func (k Kind) String() string { return string(k) }

// Binary is the executable that runs the kind ("" for Solo).
func (k Kind) Binary() string {
	if k == Herdr || k == Tmux || k == Orca {
		return string(k)
	}
	return ""
}

// Result is what a finished command left behind.
type Result = proc.Result

// Runner runs a command. A command that ran and exited non-zero is a Result
// with ExitCode set and a nil error; err means it could not run at all
// (missing binary).
type Runner func(ctx context.Context, name string, args []string) (Result, error)

// CallTimeout bounds one host command. The longest legitimate call is
// `herdr agent start/wait` with a boot timeout, which is a minute by default.
const CallTimeout = 10 * time.Minute

// ExecRunner is the production Runner.
func ExecRunner(ctx context.Context, name string, args []string) (Result, error) {
	return proc.Run(ctx, proc.Cmd{Name: name, Args: args, Timeout: CallTimeout})
}

// Deps is everything a host touches outside its own memory. Tests fill every
// field with fakes; New fills the zero fields with production values.
type Deps struct {
	Run    Runner
	Getenv func(string) string
	Sleep  func(time.Duration)
	// FS owns brief files and persistent Core/Orca identity and process evidence.
	// Nil selects OSFileSystem, including its locking and atomic writes.
	FS FileSystem
	// Alive reports whether a process exists and has not exited. A zombie
	// has exited and only waits to be reaped, so it counts as gone.
	Alive func(pid int) bool
	// Tree returns pid and all its descendants. Taken BEFORE a close:
	// afterwards orphaned children are reparented and unfindable.
	Tree func(pid int) []int
	// Start returns pid's process start time, comparable only with another read
	// from the same Start. A recorded pid whose start differs is a reused number.
	Start func(pid int) (uint64, bool)
	// KillWait is how many one-second polls Kill waits for a close to take
	// (ROTA_HOST_KILL_WAIT in the shell helper). Zero means 10.
	KillWait int
	// LookPath reports whether a binary is installed.
	LookPath func(string) (string, error)
	// Dial opens an inherited Herdr or Core API Unix socket.
	Dial func(ctx context.Context, path string) (net.Conn, error)
	// Stderr receives the one-line reports a host makes about itself (a
	// notification that never reached a human). Nil means os.Stderr.
	Stderr io.Writer
}

func (d *Deps) fill() {
	if d.FS == nil {
		d.FS = OSFileSystem{}
	}
	if d.Stderr == nil {
		d.Stderr = os.Stderr
	}
	if d.Run == nil {
		d.Run = ExecRunner
	}
	if d.Getenv == nil {
		d.Getenv = os.Getenv
	}
	if d.Sleep == nil {
		d.Sleep = time.Sleep
	}
	if d.LookPath == nil {
		d.LookPath = exec.LookPath
	}
	if d.Dial == nil {
		d.Dial = func(ctx context.Context, path string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}
	}
	if d.Alive == nil {
		d.Alive = pidlive.Alive
	}
	if d.Tree == nil {
		d.Tree = func(pid int) []int { return pidlive.Tree(pidlive.Real{}, pid) }
	}
	if d.Start == nil {
		d.Start = pidlive.StartTicks
	}
	if d.KillWait <= 0 {
		d.KillWait = 10
		if n, err := strconv.Atoi(d.Getenv("ROTA_HOST_KILL_WAIT")); err == nil && n > 0 {
			d.KillWait = n
		}
	}
}

// settle waits d for a terminal UI to react to input (a paste landing, Claude
// Code booting). ROTA_HOST_SETTLE_PCT scales it, 100 by default: the smoke
// suite drives a fake tmux that reacts at once, and sets it low (#84).
func (d *Deps) settle(dur time.Duration) {
	if pct, err := strconv.Atoi(d.Getenv("ROTA_HOST_SETTLE_PCT")); err == nil && pct >= 0 && pct < 100 {
		dur = dur * time.Duration(pct) / 100
	}
	d.Sleep(dur)
}

// Send outcomes, so callers can distinguish queued delivery from failures.
var (
	// ErrQueued acknowledges a relay delivered to a busy session's queue.
	// It is a successful delivery; callers must not resend it.
	ErrQueued = errors.New("relay queued")
	// ErrSubmitted means Core accepted input, with no proven pickup yet.
	ErrSubmitted       = errors.New("Core accepted input; pickup is not yet confirmed")
	ErrDeliveryUnknown = errors.New("Core delivery outcome unknown; inspect original terminal before recovery")
	// ErrNotSubmitted: nothing showed the brief was picked up. Safe to resend.
	ErrNotSubmitted = errors.New("brief was never submitted")
	// ErrDialogOpen: a dialog in the session refused input; nothing was sent.
	// Inspect the session before resending.
	ErrDialogOpen = errors.New("a dialog is open and refused input")
)

// SpawnOpts describes a slot session to create.
type SpawnOpts struct {
	Slot, Session, Cwd, Launch string
	// Env is KEY=VALUE pairs the agent starts with (herdr: tab create --env;
	// tmux: a prefix on the launch line). It carries the slot's account
	// variable too, which the kind's harness names (AccountEnv).
	Env         []string
	BootTimeout int // seconds
	// LinkWorkspace (herdr only): open the worktree as a workspace linked to the
	// caller's, instead of a tab in the caller's own (round.herdrGroupSlots).
	LinkWorkspace bool
	// Linked, when set, is written true if the session really went into a
	// linked workspace: a herdr that cannot link (ErrUnsupported) falls back
	// to a tab and leaves it false.
	Linked *bool
}

// ErrUnsupported marks a herdr that lacks a method or flag rota called, so a
// caller can fall back instead of failing: probed, never a version check.
var ErrUnsupported = errors.New("herdr does not support this")

// Resubmitter is implemented by a host that can tell a brief left unsent on
// the prompt line, so a resend submits it instead of typing it again.
type Resubmitter interface {
	// SubmitPending submits the file's text if it is already on the prompt
	// line. handled=false: nothing pending, the caller sends as usual.
	SubmitPending(ctx context.Context, slot, handle, file string) (handled bool, err error)
}

// Explainer is implemented by a host that can say why it classified a slot's
// agent as it did (herdr's `agent explain`).
type Explainer interface {
	// Explain returns the host's own words for the slot's state, "" when it
	// has none or the call fails.
	Explain(ctx context.Context, slot, handle string) string
}

// ErrPaneGone is what ReadPane returns when the host says the pane or its
// window no longer exists (or its server is gone): positive proof, unlike a
// capture that fails on a window that is still there.
var ErrPaneGone = errors.New("pane gone")

// PaneReader is implemented by a host that can tell a pane it could not read
// from one with no text. Capture returns "" for both.
type PaneReader interface {
	ReadPane(ctx context.Context, slot, handle string, lines int) (string, error)
}

// Labeler is implemented by a host that can show display-only text beside a
// slot's pane and the round's workspace (herdr's sidebar). It is cosmetic and
// best effort: nothing is returned, a failure never reaches the caller, and a
// host that is not a Labeler (tmux) shows nothing.
type Labeler interface {
	// Label sets the slot pane's title; "" clears it.
	Label(ctx context.Context, slot, handle, title string)
	// LabelWorkspace sets the round token of the caller's own workspace.
	LabelWorkspace(ctx context.Context, token string)
}

// Notifier is implemented by a host that can raise a user-visible notification
// (herdr). A host without one (tmux, Core, Orca) has no notification surface;
// callers ask Capabilities().Notifier instead of a Kind.
type Notifier interface {
	Notify(ctx context.Context, title, body string)
}

// Turn is a host's numbered view of an agent: its native status, the sequence
// number of its latest state change and, when that change was a finished turn,
// the same number as CompletionSeq. A field the host did not report is 0.
type Turn struct {
	Status                  string
	StateSeq, CompletionSeq int
}

// TurnReader is implemented by a host that numbers its agents' state changes
// (herdr's `state_change_seq` and `completion_seq`).
type TurnReader interface {
	// Turn reads the slot's agent; ok is false for a never-dispatched slot or a
	// failing call.
	Turn(ctx context.Context, slot, handle string) (t Turn, ok bool)
}

// DeliveryReconciler clears an unknown receipt only after the caller explicitly
// confirms from the original screen that no input was typed. It never sends input.
type DeliveryReconciler interface {
	ConfirmNotSubmitted(ctx context.Context, handle, payload string) error
}

// CoreIdentity reports the identity and diagnostic build verified by Require.
type CoreIdentity interface{ CoreIdentity() (id, version string) }

// Finished reports whether t is a turn the agent finished after the state
// change numbered base: herdr says the agent is ready for input (idle or
// done) and that this idle transition was completed work numbered past base.
// Startup, a restored conversation and a session switch carry no
// CompletionSeq, so they never count; neither does a base of 0 (none
// recorded).
func (t Turn) Finished(base int) bool {
	return base > 0 && (t.Status == "idle" || t.Status == "done") && t.CompletionSeq > base
}

// WorkspaceCloser hosts can close the linked workspace a slot was opened in
// (round.herdrGroupSlots). Only herdr has it.
type WorkspaceCloser interface {
	// CloseWorkspace closes the workspace by id once it holds no tab. A
	// workspace that is gone, or still holds a tab, is left alone.
	CloseWorkspace(ctx context.Context, id string) error
}

// Capabilities is the optional surface of a Host, one field per side. A nil
// field means the host lacks that side (tmux has no Labeler, herdr no
// Operator). It is the one place the optional interfaces are discovered.
type Capabilities struct {
	CoreIdentity       CoreIdentity
	DeliveryReconciler DeliveryReconciler
	Resubmitter        Resubmitter
	Explainer          Explainer
	PaneReader         PaneReader
	Labeler            Labeler
	Notifier           Notifier
	TurnReader         TurnReader
	Watcher            Watcher
	PaneSweeper        PaneSweeper
	Layouter           Layouter
	TabOpener          TabOpener
	SessionStarter     SessionStarter
	TabLister          TabLister
	TabFollower        TabFollower
	PaneHost           PaneHost
	OutputWatcher      OutputWatcher
	Operator           Operator
	Snapshotter        Snapshotter
	WorkspaceCloser    WorkspaceCloser
	LinkedLayouter     LinkedLayouter
	// RuntimeScopedHandles: a recorded handle dies with the host runtime while
	// the agent may live on, so a handle missing from the live list proves
	// nothing about the agent (Orca).
	RuntimeScopedHandles bool
	// TreeKillClose: closing the session must kill the agent's process tree
	// and prove it gone, even after a positive exit poll (Orca).
	TreeKillClose bool

	// Policy below is data per Kind (policy.go), not an optional side.

	// InSessionRule is non-nil for a host that only works from inside one of
	// its own panes (herdr).
	InSessionRule *InSessionRule
	// LaunchKindBound: the host starts the agent by the launch binary's own
	// kind, so a launch line for another kind is refused (herdr).
	LaunchKindBound bool
	// NoNominalHandle: a slot has no session until its first dispatch, so
	// pool init records no handle (a nominal one reads as a dead session).
	NoNominalHandle bool
	// InPlace: an orchestrator runs in the current terminal (Core).
	InPlace bool
	// Harness is what a worker harness may read about the host.
	Harness harness.HostTraits
	// Server is the host's server process name; zero when it has none to avoid.
	Server ServerProc
	// Attach builds the command that starts or attaches to a named session;
	// nil for a host with no sessions of its own.
	Attach SessionAttach
	// Diagnose checks the host is installed and usable (rota doctor).
	Diagnose func(Probe) Health
}

// RuntimeScoped is implemented by hosts whose handles do not outlive the host
// runtime; see Capabilities.RuntimeScopedHandles.
type RuntimeScoped interface{ RuntimeScopedHandles() bool }

// TreeKiller is implemented by hosts whose close needs a process-tree kill;
// see Capabilities.TreeKillClose.
type TreeKiller interface{ TreeKillClose() bool }

// CapabilitiesForKind is the capabilities of the adapter for k, read without
// building it, so it always agrees with New(k, ...).Capabilities(). Solo and
// unknown kinds have none.
func CapabilitiesForKind(k Kind) Capabilities {
	switch k {
	case Herdr:
		return (&herdr{}).Capabilities()
	case HerdrCore:
		return (&coreHost{}).Capabilities()
	case Tmux:
		return (&tmux{}).Capabilities()
	case Orca:
		return (&orca{}).Capabilities()
	}
	return Capabilities{}
}

// CanGroup reports whether the host can keep slots in linked workspaces
// (round.herdrGroupSlots): open them, close them and move panes back in. The one
// test dispatch and layout both apply.
func (c Capabilities) CanGroup() bool {
	return c.WorkspaceCloser != nil && c.LinkedLayouter != nil
}

// CapabilitiesOf reads the optional sides h implements. Real hosts return it
// from Capabilities; a test fake does the same to declare what it supports.
func CapabilitiesOf(h any) Capabilities {
	var c Capabilities
	c.CoreIdentity, _ = h.(CoreIdentity)
	c.DeliveryReconciler, _ = h.(DeliveryReconciler)
	c.Resubmitter, _ = h.(Resubmitter)
	c.Explainer, _ = h.(Explainer)
	c.PaneReader, _ = h.(PaneReader)
	c.Labeler, _ = h.(Labeler)
	c.Notifier, _ = h.(Notifier)
	c.TurnReader, _ = h.(TurnReader)
	c.Watcher, _ = h.(Watcher)
	c.PaneSweeper, _ = h.(PaneSweeper)
	c.Layouter, _ = h.(Layouter)
	c.TabOpener, _ = h.(TabOpener)
	c.SessionStarter, _ = h.(SessionStarter)
	c.TabLister, _ = h.(TabLister)
	c.TabFollower, _ = h.(TabFollower)
	c.WorkspaceCloser, _ = h.(WorkspaceCloser)
	c.LinkedLayouter, _ = h.(LinkedLayouter)
	c.PaneHost, _ = h.(PaneHost)
	c.OutputWatcher, _ = h.(OutputWatcher)
	c.Operator, _ = h.(Operator)
	c.Snapshotter, _ = h.(Snapshotter)
	if r, ok := h.(RuntimeScoped); ok {
		c.RuntimeScopedHandles = r.RuntimeScopedHandles()
	}
	if t, ok := h.(TreeKiller); ok {
		c.TreeKillClose = t.TreeKillClose()
	}
	return c
}

// Host is one terminal backend.
type Host interface {
	// Kind is the backend this host drives (Herdr, HerdrCore, Tmux or Orca).
	Kind() Kind
	// Capabilities names the optional sides this host supports; a field is
	// nil for a side it lacks. Callers ask here instead of type-asserting.
	Capabilities() Capabilities
	// Require fails when the backend's binary is missing.
	Require() error
	// InSession: this process runs inside a managed pane of the backend.
	InSession() bool
	// Where names the session for status lines.
	Where() string
	// Spawn creates the slot's session and returns its handle. A Spawn that
	// fails with its session still open returns that handle beside the error,
	// so the caller can record it and a later dispatch reaps it; "" means no
	// session remains.
	Spawn(ctx context.Context, o SpawnOpts) (string, error)
	// Send submits the file as a prompt and confirms pickup. The error is
	// ErrQueued (successful queued delivery), ErrNotSubmitted, ErrDialogOpen,
	// or another failure.
	Send(ctx context.Context, slot, handle, file string) error
	// Capture returns recent pane text, "" for a slot without a handle.
	Capture(ctx context.Context, slot, handle string, lines int) string
	// Status is the backend's native agent state, "" when it has none.
	Status(ctx context.Context, slot, handle string) string
	// Kill destroys the slot's session and proves it gone.
	Kill(ctx context.Context, slot, handle string) error
}

// ErrUnknownKind is what the Host New returns for Solo or an unrecognized kind
// answers with: those kinds have no terminal to drive.
var ErrUnknownKind = errors.New("no terminal host for this kind")

// New returns the selected terminal adapter. Solo and unknown kinds get a Host
// that refuses every operation with ErrUnknownKind, so a caller bug fails at
// its cause instead of driving tmux.
func New(kind Kind, d Deps) Host {
	d.fill()
	if kind == HerdrCore {
		return &coreHost{d: d}
	}
	if kind == Orca {
		return &orca{d: d}
	}
	if kind == Herdr {
		return &herdr{d: d, noted: new(atomic.Bool)}
	}
	if kind == Tmux {
		return &tmux{d: d}
	}
	return refused{kind: kind}
}

// refused is the Host for Solo and unknown kinds: it drives nothing and every
// operation that can fail says why.
type refused struct{ kind Kind }

func (r refused) err() error { return fmt.Errorf("%w: %q", ErrUnknownKind, string(r.kind)) }

func (r refused) Kind() Kind                 { return r.kind }
func (r refused) Capabilities() Capabilities { return Capabilities{} }
func (r refused) Require() error             { return r.err() }
func (r refused) InSession() bool            { return false }
func (r refused) Where() string              { return "" }
func (r refused) Spawn(context.Context, SpawnOpts) (string, error) {
	return "", r.err()
}
func (r refused) Send(context.Context, string, string, string) error  { return r.err() }
func (r refused) Capture(context.Context, string, string, int) string { return "" }
func (r refused) Status(context.Context, string, string) string       { return "" }
func (r refused) Kill(context.Context, string, string) error          { return r.err() }

// presence is what a host query says about a target. Only presentGone proves
// cleanup; presentUnknown (transport, execution or unreadable-response
// failure) never does.
type presence int

const (
	presentYes presence = iota
	presentGone
	presentUnknown
)

// seenBool maps a boolean "is it gone" check, for adapters whose query cannot
// fail ambiguously, onto presence.
func seenBool(gone bool) presence {
	if gone {
		return presentGone
	}
	return presentYes
}

// killLoop is the proof half of Kill, shared by the hosts: poll until gone()
// reports presentGone and every recorded pid has exited, or give up after
// KillWait polls. unknown says the last query could not tell, so the caller
// can report an unverified close rather than a surviving one.
func killLoop(d *Deps, pids []int, gone func() presence) (alive []int, unknown, ok bool) {
	for i := 0; ; i++ {
		alive = alive[:0]
		for _, p := range pids {
			if d.Alive(p) {
				alive = append(alive, p)
			}
		}
		g := gone()
		if g == presentGone && len(alive) == 0 {
			return nil, false, true
		}
		if i+1 >= d.KillWait {
			return alive, g == presentUnknown, false
		}
		d.Sleep(time.Second)
	}
}

// killSteps is what an adapter contributes to the shared kill sequence: the
// process evidence it read before the close, how to close its handle, and how
// to ask whether the handle is gone. runKill owns the ordering around them.
type killSteps struct {
	// deps supplies liveness and the poll clock (an adapter that probes
	// retained identity passes its own).
	deps *Deps
	// pids were read before the close. unverifiedBefore says the query that
	// should have read them failed, so no later absence can prove cleanup.
	pids             []int
	unverifiedBefore bool
	// close destroys the handle; an error ends the kill without proof.
	close func() error
	// gone reports whether the handle no longer exists.
	gone func() presence
}

// killOutcome is runKill's verdict: the pids still alive, and whether the last
// query could not tell (unknown) or everything is proved gone (ok).
type killOutcome struct {
	alive            []int
	unknown, ok      bool
	unverifiedBefore bool
}

// runKill is the one kill ordering: close the handle, then poll until both the
// handle and every pre-recorded pid are gone. A pre-close query that failed
// downgrades even a clean poll to unproved.
func runKill(k killSteps) (killOutcome, error) {
	if err := k.close(); err != nil {
		return killOutcome{}, err
	}
	alive, unknown, ok := killLoop(k.deps, k.pids, k.gone)
	return killOutcome{alive: alive, unknown: unknown, ok: ok && !k.unverifiedBefore, unverifiedBefore: k.unverifiedBefore}, nil
}

// err words the outcome for the hosts that answer in a window or tab: nil when
// proved, else the "could not be verified" or "still running" refusal. tool
// names the query that failed ("tmux"), target the handle ("window w1:2").
func (o killOutcome) err(slot, tool, target string) error {
	switch {
	case o.ok:
		return nil
	case o.unverifiedBefore:
		return fmt.Errorf("slot '%s' previous session could not be verified (%s): %s query failed before the close, so no process evidence was collected; not spawning a second one",
			slot, target, tool)
	case o.unknown:
		return fmt.Errorf("slot '%s' previous session could not be verified (%s%s): %s query failed after the close; not spawning a second one",
			slot, target, pidSuffix(o.alive), tool)
	}
	return fmt.Errorf("slot '%s' previous session is still running (%s%s); not spawning a second one",
		slot, target, pidSuffix(o.alive))
}

func pidList(pids []int) string {
	var b strings.Builder
	for _, p := range pids {
		b.WriteString(" " + strconv.Itoa(p))
	}
	return b.String()
}

func (d *Deps) readFile(p string) (string, error) {
	b, err := d.FS.ReadFile(p)
	return string(b), err
}
