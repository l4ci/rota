// Package host drives the terminal host that runs /rota-work worker sessions:
// tmux windows, herdr tabs or Orca terminals. It ports the bin/hv-host-*.sh
// helpers and adds the opt-in Orca adapter.
//
// SAFETY: closing a herdr tab, tmux window or Orca terminal kills live agents.
// Nothing here execs a binary directly:
// every command goes through Deps.Run, which tests replace with a fake. The
// default runner (ExecRunner) is the only place that reaches os/exec.
package host

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

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
	Herdr Kind = "herdr"
	Tmux  Kind = "tmux"
	Orca  Kind = "orca"
)

// KindOf is the kind a config or registry string names: "herdr", "tmux", "orca" and "solo",
// anything else (including "subagent" and "") the zero Kind.
func KindOf(s string) Kind {
	switch k := Kind(s); k {
	case Herdr, Tmux, Orca, Solo:
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
	// FS owns brief files and Orca's persistent identity and process evidence.
	// Nil selects OSFileSystem, including its locking and atomic writes.
	FS FileSystem
	// Alive reports whether a process exists and has not exited. A zombie
	// has exited and only waits to be reaped, so it counts as gone.
	Alive func(pid int) bool
	// Tree returns pid and all its descendants. Taken BEFORE a close:
	// afterwards orphaned children are reparented and unfindable.
	Tree func(pid int) []int
	// KillWait is how many one-second polls Kill waits for a close to take
	// (ROTA_HOST_KILL_WAIT in the shell helper). Zero means 10.
	KillWait int
	// LookPath reports whether a binary is installed.
	LookPath func(string) (string, error)
	// Dial opens the herdr API socket (a unix socket path).
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
	Resubmitter     Resubmitter
	Explainer       Explainer
	PaneReader      PaneReader
	Labeler         Labeler
	TurnReader      TurnReader
	Watcher         Watcher
	PaneSweeper     PaneSweeper
	Layouter        Layouter
	TabOpener       TabOpener
	SessionStarter  SessionStarter
	TabLister       TabLister
	TabFollower     TabFollower
	PaneHost        PaneHost
	OutputWatcher   OutputWatcher
	Operator        Operator
	Snapshotter     Snapshotter
	WorkspaceCloser WorkspaceCloser
	LinkedLayouter  LinkedLayouter
	// RuntimeScopedHandles: a recorded handle dies with the host runtime while
	// the agent may live on, so a handle missing from the live list proves
	// nothing about the agent (Orca).
	RuntimeScopedHandles bool
	// TreeKillClose: closing the session must kill the agent's process tree
	// and prove it gone, even after a positive exit poll (Orca).
	TreeKillClose bool
}

// RuntimeScoped is implemented by hosts whose handles do not outlive the host
// runtime; see Capabilities.RuntimeScopedHandles.
type RuntimeScoped interface{ RuntimeScopedHandles() bool }

// TreeKiller is implemented by hosts whose close needs a process-tree kill;
// see Capabilities.TreeKillClose.
type TreeKiller interface{ TreeKillClose() bool }

// CapabilitiesForKind is the capabilities of the adapter for k, read without
// building it. Solo and unknown kinds have none.
func CapabilitiesForKind(k Kind) Capabilities {
	if k == Orca {
		return CapabilitiesOf(&orca{})
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
	c.Resubmitter, _ = h.(Resubmitter)
	c.Explainer, _ = h.(Explainer)
	c.PaneReader, _ = h.(PaneReader)
	c.Labeler, _ = h.(Labeler)
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
	// Kind is the backend this host drives (Herdr, Tmux or Orca).
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
	// Notify raises a notification where the backend has one.
	Notify(ctx context.Context, title, body string)
}

// New returns the adapter for Herdr or Orca; every other kind, including Solo,
// selects tmux (the worker helpers predate round.dispatch=herdr).
func New(kind Kind, d Deps) Host {
	d.fill()
	if kind == Orca {
		return &orca{d: d}
	}
	if kind == Herdr {
		return &herdr{d: d, noted: new(atomic.Bool)}
	}
	return &tmux{d: d}
}

// killLoop is the proof half of Kill, shared by both hosts: poll until gone()
// is true and every recorded pid has exited, or give up after KillWait polls.
func killLoop(d *Deps, pids []int, gone func() bool) (alive []int, ok bool) {
	for i := 0; ; i++ {
		alive = alive[:0]
		for _, p := range pids {
			if d.Alive(p) {
				alive = append(alive, p)
			}
		}
		if gone() && len(alive) == 0 {
			return nil, true
		}
		if i+1 >= d.KillWait {
			return alive, false
		}
		d.Sleep(time.Second)
	}
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
