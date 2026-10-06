package host

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// tmux ports bin/hv-host-tmux.sh. A slot's handle is its window target,
// `<session>:<slot>`, stable across dispatches: the window is killed and
// recreated under the same name.
//
// The paste path has three traps in it, which is why it lives here once:
//  1. A long prompt sent via send-keys arrives as a bracketed paste that
//     swallows its own trailing Enter; load-buffer/paste-buffer sidesteps it.
//  2. Enter must be a SEPARATE keypress, and even then a pasted prompt can
//     land as a collapsed paste chip that the first Enter does not submit.
//  3. Pickup must be CONFIRMED by re-reading the pane.
//
// Captures use -J so wrapped lines are joined; without it a pane comparison
// is against hard-wrapped text and long lines read as changed when they are not.
type tmux struct{ d Deps }

func (t *tmux) Name() string { return "tmux" }

func (t *tmux) Require() error {
	if _, err := t.d.LookPath("tmux"); err != nil {
		return fmt.Errorf("tmux is not installed")
	}
	return nil
}

// InSession keys on $TMUX: `tmux has-session` answers whether a session
// EXISTS, which is a different question from whether WE are in it.
func (t *tmux) InSession() bool { return t.d.Getenv("TMUX") != "" }

func (t *tmux) Where() string {
	r, err := t.d.Run(context.Background(), "tmux", []string{"display-message", "-p", "#{session_name}"})
	if err != nil || r.ExitCode != 0 || strings.TrimRight(r.Stdout, "\n") == "" {
		return "?"
	}
	return strings.TrimRight(r.Stdout, "\n")
}

func (t *tmux) tmux(ctx context.Context, args ...string) Result {
	r, err := t.d.Run(ctx, "tmux", args)
	if err != nil {
		return Result{ExitCode: 127, Stderr: err.Error()}
	}
	return r
}

func (t *tmux) pane(ctx context.Context, window string) string {
	return t.tmux(ctx, "capture-pane", "-pJ", "-t", window).Stdout
}

// waitReady blocks until the pane looks like a booted Claude Code UI.
// Pasting into a shell that has not yet handed off to Claude Code loses the
// text silently, which is the failure this exists to prevent.
func (t *tmux) waitReady(ctx context.Context, window string, timeout int) bool {
	for waited := 0; waited < timeout; waited += 2 {
		p := t.pane(ctx, window)
		if looksBooted(p) {
			return true
		}
		t.d.settle(2 * time.Second)
	}
	return false
}

// bootPatterns is the one place that says what a booted agent pane looks
// like; any match counts. Each entry is tied to the UI it was seen on. A
// dialog (folder trust) must match none of them, so no bare prompt glyph.
var bootPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)\?.*for shortcuts`),    // Claude Code, older hint line
	regexp.MustCompile(`Welcome to Claude Code`),   // Claude Code, older banner
	regexp.MustCompile(`╭─`),                       // older box-drawn frame (Claude Code, Codex)
	regexp.MustCompile(`Claude Code v\d+\.\d+`),    // Claude Code 2.1.289 banner
	regexp.MustCompile(`Ask Codex to do anything`), // Codex 0.159.2 prompt placeholder
}

func looksBooted(p string) bool {
	for _, re := range bootPatterns {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

func (t *tmux) Spawn(ctx context.Context, o SpawnOpts) (string, error) {
	handle := o.Session + ":" + o.Slot
	launch := o.Launch
	if o.ConfigDir != "" {
		launch = "CLAUDE_CONFIG_DIR=" + o.ConfigDir + " " + launch
	}
	if t.tmux(ctx, "has-session", "-t", o.Session).ExitCode != 0 {
		t.tmux(ctx, "new-session", "-d", "-s", o.Session, "-c", o.Cwd)
	}
	if r := t.tmux(ctx, "new-window", "-d", "-t", o.Session, "-n", o.Slot, "-c", o.Cwd); r.ExitCode != 0 {
		return "", fmt.Errorf("could not create tmux window %s", handle)
	}
	t.tmux(ctx, "send-keys", "-t", handle, launch, "C-m")
	if !t.waitReady(ctx, handle, o.BootTimeout) {
		return "", fmt.Errorf("slot '%s' session did not come up within %ds", o.Slot, o.BootTimeout)
	}
	return handle, nil
}

// Send pastes the file's contents, submits, and confirms the pane changed.
// It gives up after 4 attempts. A human draft on the prompt line is waited out
// and then refused (ErrDraftOnPrompt) with nothing pasted.
func (t *tmux) Send(ctx context.Context, slot, handle, file string) error {
	return t.sendFile(ctx, handle, file, "rota-"+slot)
}

func (t *tmux) sendFile(ctx context.Context, handle, file, buf string) error {
	// A human typing in the pane would get the brief pasted into their draft.
	text, _ := readFile(file)
	text = strings.TrimRight(text, "\n")
	if waitNoDraft(&t.d, func() string { return t.pane(ctx, handle) }, text) != "" {
		return ErrDraftOnPrompt
	}
	before := t.pane(ctx, handle)
	if t.tmux(ctx, "load-buffer", "-b", buf, file).ExitCode != 0 {
		return ErrNotSubmitted
	}
	if t.tmux(ctx, "paste-buffer", "-b", buf, "-t", handle).ExitCode != 0 {
		return ErrNotSubmitted
	}
	t.tmux(ctx, "delete-buffer", "-b", buf)
	for tries := 0; tries < 4; tries++ {
		t.d.settle(time.Second)
		t.tmux(ctx, "send-keys", "-t", handle, "C-m")
		t.d.settle(2 * time.Second)
		if t.pane(ctx, handle) != before {
			return nil
		}
	}
	return ErrNotSubmitted
}

// Capture: an empty target would capture the CALLER's pane, so a slot with
// no handle yields nothing. lines is unused: capture-pane takes the visible
// pane, as the shell host did.
func (t *tmux) Capture(ctx context.Context, slot, handle string, lines int) string {
	if handle == "" {
		return ""
	}
	return t.pane(ctx, handle)
}

// Draft implements Drafter.
func (t *tmux) Draft(ctx context.Context, slot, handle, file string) string {
	text, _ := readFile(file)
	return humanDraft(t.pane(ctx, handle), strings.TrimRight(text, "\n"))
}

// Status: tmux has no native agent state, so the classifier decides from
// pane text and movement alone.
func (t *tmux) Status(ctx context.Context, slot, handle string) string { return "" }

// windowPID is the pane PID of the window with that exact name, 0 when there
// is none. Exact match: a bare -t target would also take a prefix, so `w1`
// could be answered by `w10`.
func (t *tmux) windowPID(ctx context.Context, handle string) int {
	session, name, _ := strings.Cut(handle, ":")
	r := t.tmux(ctx, "list-windows", "-t", session, "-F", "#{window_name} #{pane_pid}")
	for _, line := range strings.Split(r.Stdout, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == name {
			pid, _ := strconv.Atoi(f[1])
			return pid
		}
	}
	return 0
}

// Kill destroys the slot's window and proves it is gone: it records the pane
// PID and its descendants first (claude plus its MCP children) and fails
// unless the window no longer exists and every recorded PID has exited. A
// swallowed failure here would leave the old session running beside the next
// one in the same worktree.
func (t *tmux) Kill(ctx context.Context, slot, handle string) error {
	if handle == "" {
		return nil
	}
	var pids []int
	if pid := t.windowPID(ctx, handle); pid != 0 {
		pids = t.d.Tree(pid)
	}
	t.tmux(ctx, "kill-window", "-t", handle)
	alive, ok := killLoop(&t.d, pids, func() bool { return t.windowPID(ctx, handle) == 0 })
	if ok {
		return nil
	}
	return fmt.Errorf("slot '%s' previous session is still running (window %s%s); not spawning a second one",
		slot, handle, pidSuffix(alive))
}

func pidSuffix(alive []int) string {
	if len(alive) == 0 {
		return ""
	}
	return ", pids" + pidList(alive)
}

// Notify: tmux has no notification surface.
func (t *tmux) Notify(ctx context.Context, title, body string) {}

// OperatorOpts describes the operator window `worker session ensure` opens
// when the orchestrator is not inside tmux.
type OperatorOpts struct {
	Session     string
	Root        string // the project root, the window's cwd
	Command     string // what the operator window runs
	Instruction string // file pasted into it once it boots; "" for none
	BootTimeout int
}

// Operator hosts can open an operator window (tmux only: herdr has nothing to
// hand off to, since worker tabs open in the caller's own workspace).
type Operator interface {
	EnsureOperator(ctx context.Context, o OperatorOpts) error
}

// Operator failures, distinguished so the verb can word its hint.
var (
	ErrOperatorSession = fmt.Errorf("could not create the tmux session")
	ErrOperatorWindow  = fmt.Errorf("could not create the operator window")
	ErrOperatorBoot    = fmt.Errorf("the operator session did not come up")
	ErrOperatorSend    = fmt.Errorf("the operator never picked up its instruction")
)

// EnsureOperator spawns an operator window, starts the operator command in it
// and, with an instruction file, pastes it once the UI is up. An existing
// operator window is replaced, not stacked: a second ensure after an
// interrupted run must not leave two operators.
func (t *tmux) EnsureOperator(ctx context.Context, o OperatorOpts) error {
	if t.tmux(ctx, "has-session", "-t", o.Session).ExitCode != 0 {
		if t.tmux(ctx, "new-session", "-d", "-s", o.Session, "-c", o.Root, "-n", "scratch").ExitCode != 0 {
			return ErrOperatorSession
		}
	}
	for _, name := range strings.Split(t.tmux(ctx, "list-windows", "-t", o.Session, "-F", "#{window_name}").Stdout, "\n") {
		if name == "operator" {
			t.tmux(ctx, "kill-window", "-t", o.Session+":operator")
			break
		}
	}
	if t.tmux(ctx, "new-window", "-d", "-t", o.Session, "-n", "operator", "-c", o.Root).ExitCode != 0 {
		return ErrOperatorWindow
	}
	target := o.Session + ":operator"
	t.tmux(ctx, "send-keys", "-t", target, o.Command, "C-m")
	if o.Instruction == "" {
		return nil
	}
	if !t.waitReady(ctx, target, o.BootTimeout) {
		return ErrOperatorBoot
	}
	if t.sendFile(ctx, target, o.Instruction, "rota-operator") != nil {
		return ErrOperatorSend
	}
	return nil
}
