// Package host drives the terminal host that runs /rota-work worker sessions:
// tmux windows or herdr tabs. It is the Go port of bin/hv-host-select.sh,
// bin/hv-host-tmux.sh and bin/hv-host-herdr.sh.
//
// SAFETY: a round runs inside herdr and tmux, and a stray `herdr tab close` or
// `tmux kill-window` kills live agents. Nothing here execs a binary directly:
// every command goes through Deps.Run, which tests replace with a fake. The
// default runner (ExecRunner) is the only place that reaches os/exec.
package host

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Result is what a finished command left behind.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner runs a command. A command that ran and exited non-zero is a Result
// with ExitCode set and a nil error; err means it could not run at all
// (missing binary).
type Runner func(ctx context.Context, name string, args []string) (Result, error)

// CallTimeout bounds one host command. The longest legitimate call is
// `herdr agent start/wait` with a boot timeout, which is a minute by default.
const CallTimeout = 10 * time.Minute

// ExecRunner is the production Runner.
func ExecRunner(ctx context.Context, name string, args []string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := Result{Stdout: out.String(), Stderr: errb.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.ExitCode = ee.ExitCode()
		return r, nil
	}
	return r, err
}

// Deps is everything a host touches outside its own memory. Tests fill every
// field with fakes; New fills the zero fields with production values.
type Deps struct {
	Run    Runner
	Getenv func(string) string
	Sleep  func(time.Duration)
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
}

func (d *Deps) fill() {
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
		d.Alive = func(pid int) bool { return pidAlive(d.Run, pid) }
	}
	if d.Tree == nil {
		d.Tree = func(pid int) []int { return pidTree(d.Run, pid) }
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

// Sentinel failures of Send, so callers can map them to exit codes.
var (
	// ErrNotSubmitted: nothing showed the brief was picked up. Safe to resend.
	ErrNotSubmitted = errors.New("brief was never submitted")
	// ErrDialogOpen: a dialog in the session refused input; nothing was sent.
	// Inspect the session before resending.
	ErrDialogOpen = errors.New("a dialog is open and refused input")
)

// SpawnOpts describes a slot session to create.
type SpawnOpts struct {
	Slot, Session, Cwd, ConfigDir, Launch string
	// CodexHome is a codex launch's CODEX_HOME (herdr passes it to the tab).
	CodexHome   string
	BootTimeout int // seconds
}

// Resubmitter is implemented by a host that can tell a brief left unsent on
// the prompt line, so a resend submits it instead of typing it again.
type Resubmitter interface {
	// SubmitPending submits the file's text if it is already on the prompt
	// line. handled=false: nothing pending, the caller sends as usual.
	SubmitPending(ctx context.Context, slot, handle, file string) (handled bool, err error)
}

// Host is one terminal backend.
type Host interface {
	// Name is "tmux" or "herdr".
	Name() string
	// Require fails when the backend's binary is missing.
	Require() error
	// InSession: this process runs inside a managed pane of the backend.
	InSession() bool
	// Where names the session for status lines.
	Where() string
	// Spawn creates the slot's session and returns its handle.
	Spawn(ctx context.Context, o SpawnOpts) (string, error)
	// Send submits the file as a prompt and confirms pickup. The error is
	// ErrNotSubmitted, ErrDialogOpen, or another failure.
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

// New returns the host for work.dispatch: "herdr" selects herdr, anything
// else, including the default "subagent", selects tmux (the worker helpers
// predate work.dispatch=herdr and have always driven tmux).
func New(dispatch string, d Deps) Host {
	d.fill()
	if dispatch == "herdr" {
		return &herdr{d: d}
	}
	return &tmux{d: d}
}

// pidAlive: kill -0 succeeds for a zombie, so the process state is checked.
func pidAlive(run Runner, pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	r, err := run(context.Background(), "ps", []string{"-o", "stat=", "-p", strconv.Itoa(pid)})
	if err != nil {
		return true
	}
	stat := strings.TrimSpace(r.Stdout)
	return stat != "" && !strings.HasPrefix(stat, "Z")
}

// pidTree lists pid and its descendants, breadth first.
func pidTree(run Runner, root int) []int {
	r, err := run(context.Background(), "ps", []string{"-A", "-o", "pid=", "-o", "ppid="})
	out := []int{root}
	if err != nil {
		return out
	}
	kids := map[int][]int{}
	for _, line := range strings.Split(r.Stdout, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		p, e1 := strconv.Atoi(f[0])
		pp, e2 := strconv.Atoi(f[1])
		if e1 == nil && e2 == nil {
			kids[pp] = append(kids[pp], p)
		}
	}
	for i := 0; i < len(out); i++ {
		out = append(out, kids[out[i]]...)
	}
	return out
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

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
