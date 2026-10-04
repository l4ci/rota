package host

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/shlex"
)

// herdr ports bin/hv-host-herdr.sh. A herdr slot is a TAB in the current
// workspace whose root pane runs Claude Code in the slot's worktree. The
// handle is the tab id (`w1:t7`). Tab ids are never reused, so the handle
// changes on every dispatch and the caller must persist it. The agent's name
// derives from slot + handle (`rota-w1-w1-t7`): herdr agent names are unique per
// SERVER, and a bare `w1` would collide with another repo's pool. See AgentName
// for the names herdr accepts.
//
// Every herdr command prints JSON on stdout and, on failure, a JSON error on
// stderr with exit 1. herdr reports agent state natively, so none of tmux's
// paste tricks apply: `agent prompt` submits text and Enter as one write.
type herdr struct{ d Deps }

func (h *herdr) Name() string { return "herdr" }

func (h *herdr) Require() error {
	if _, err := h.d.LookPath("herdr"); err != nil {
		return fmt.Errorf("herdr is not installed")
	}
	return nil
}

// InSession: herdr injects HERDR_ENV=1 into every pane it manages.
// Controlling a herdr server from outside one is what herdr's own guide
// forbids: commands then land in whatever workspace a human has focused.
func (h *herdr) InSession() bool {
	return h.d.Getenv("HERDR_ENV") == "1" && h.d.Getenv("HERDR_WORKSPACE_ID") != ""
}

func (h *herdr) Where() string {
	ws := h.d.Getenv("HERDR_WORKSPACE_ID")
	if ws == "" {
		ws = "?"
	}
	return "herdr workspace " + ws
}

// agentNameRe is what herdr 0.9.3 accepts as an agent name.
var agentNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// AgentName is the herdr agent name for a slot and handle: `rota-<slot>-<tab id>`
// (`rota-w1-w1-t7`) when herdr accepts that, which keeps the name of every agent
// started before this rule. Workspace ids are mixed case (`w1W`) and herdr
// takes only [a-z][a-z0-9_-]{0,31}, so otherwise it is `rota-<slot>-<hash>`: the
// slot lowercased with anything else turned into `-` and cut to 18, and the
// first 8 hex of the handle's SHA-1, which stays unique when two workspace ids
// differ only in case.
func AgentName(slot, handle string) string {
	if n := "rota-" + slot + "-" + strings.ReplaceAll(handle, ":", "-"); agentNameRe.MatchString(n) {
		return n
	}
	var b strings.Builder
	for _, r := range strings.ToLower(slot) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	s := b.String()
	if len(s) > 18 {
		s = s[:18]
	}
	sum := sha1.Sum([]byte(handle))
	return "rota-" + s + "-" + hex.EncodeToString(sum[:])[:8]
}

func (h *herdr) herdr(ctx context.Context, args ...string) Result {
	r, err := h.d.Run(ctx, "herdr", args)
	if err != nil {
		return Result{ExitCode: 127, Stderr: err.Error()}
	}
	return r
}

// jget reads the value at a dotted key path from a herdr JSON reply, "" on a
// missing key, bad JSON, or null.
func jget(doc, path string) string {
	var v any
	if json.Unmarshal([]byte(doc), &v) != nil {
		return ""
	}
	for _, k := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		if v, ok = m[k]; !ok {
			return ""
		}
	}
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// LaunchArgs splits a worker launch command for `herdr agent start --kind
// <kind>`, which runs the agent binary itself and takes only its arguments.
// The kind is the binary's basename after any leading KEY=VALUE tokens:
// `claude` or `codex`. Those tokens are returned as env (passed to the tab as
// --env), everything after the binary as args. It fails when the command
// launches anything else, since agent start cannot run it.
func LaunchArgs(launch string) (kind string, env, args []string, err error) {
	toks, err := shlex.Split(launch)
	if err != nil {
		return "", nil, nil, err
	}
	i := 0
	for i < len(toks) && envAssign.MatchString(toks[i]) {
		env = append(env, toks[i])
		i++
	}
	if i < len(toks) {
		if h, ok := harness.ForBinary(baseName(toks[i])); ok {
			return h.Kind(), env, toks[i+1:], nil
		}
	}
	return "", nil, nil, fmt.Errorf("not a %s launch", harness.KindList())
}

var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// dialogLine is one option of a startup dialog: an optional cursor (❯, or >
// on a plain terminal), an optional number (Claude Code numbered its options
// before v2.1.288), then the text.
var dialogLine = regexp.MustCompile(`^\s*(❯|>)?\s*(?:\d+\.\s+)?(.*\S)`)

var dialogAccept = []string{"Yes, I trust this folder", "Yes, I accept", "Yes, proceed"}

// numbered is an option line that carries a number.
var numbered = regexp.MustCompile(`^\s*(❯|>)?\s*\d+\.\s+\S`)

// DialogKeys reads a startup dialog off the pane text. Claude Code can open
// on the folder-trust prompt for a fresh worktree, or the Bypass Permissions
// warning on a config dir that has not accepted it yet. The two put their
// accepting option in different positions, so a fixed keypress picks "No,
// exit" on one of them. The options are the block of adjacent non-blank lines
// that holds the cursor (❯), numbered or not; with no cursor on screen, the
// block of numbered lines, cursor on the first. It returns the keys that move
// from the cursor to the accepting option plus `enter`; ok is false on an
// unknown dialog.
func DialogKeys(pane string) (keys []string, ok bool) {
	var blocks [][]string
	var cur []string
	for _, line := range strings.Split(pane, "\n") {
		if strings.TrimSpace(line) == "" {
			if cur != nil {
				blocks, cur = append(blocks, cur), nil
			}
			continue
		}
		cur = append(cur, line)
	}
	if cur != nil {
		blocks = append(blocks, cur)
	}
	var opts []string
	cursor := -1
	for _, b := range blocks {
		for i, line := range b {
			if m := dialogLine.FindStringSubmatch(line); m != nil && m[1] != "" {
				opts, cursor = b, i
			}
		}
	}
	if opts == nil {
		for _, b := range blocks {
			if numbered.MatchString(b[0]) {
				opts, cursor = b, 0
				break
			}
		}
	}
	target := -1
	for i, line := range opts {
		m := dialogLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		for _, a := range dialogAccept {
			if strings.Contains(m[2], a) {
				target = i
				break
			}
		}
		if target >= 0 {
			break
		}
	}
	if target < 0 {
		return nil, false
	}
	step, n := "down", target-cursor
	if target < cursor {
		step, n = "up", cursor-target
	}
	for i := 0; i < n; i++ {
		keys = append(keys, step)
	}
	return append(keys, "enter"), true
}

// Spawn adopts the worktree as a new background tab, starts the agent in
// it, clears any startup dialog, and waits for it to idle. It returns the tab
// id (the handle). Session is unused: herdr tabs live in the caller's own
// workspace.
func (h *herdr) Spawn(ctx context.Context, o SpawnOpts) (string, error) {
	kind, env, agentArgs, err := LaunchArgs(o.Launch)
	if err != nil {
		return "", fmt.Errorf("work.dispatch=herdr launches %s itself; the launch command must run one of them, got: %s", harness.KindList(), o.Launch)
	}
	args := []string{"tab", "create", "--workspace", h.d.Getenv("HERDR_WORKSPACE_ID"),
		"--cwd", o.Cwd, "--label", o.Slot, "--no-focus"}
	for _, e := range env {
		args = append(args, "--env", e)
	}
	// Account selection must happen at tab creation: agent start runs the
	// binary directly and ignores shell aliases or wrappers. What selects the
	// account is the harness's own business: a codex slot's CODEX_HOME, a
	// claude slot's config dir.
	hz, _ := harness.Lookup(kind)
	for _, e := range hz.AccountEnv(harness.Account{ConfigDir: o.ConfigDir, CodexHome: o.CodexHome}) {
		args = append(args, "--env", e)
	}
	r := h.herdr(ctx, args...)
	if r.ExitCode != 0 {
		return "", fmt.Errorf("herdr tab create failed for slot '%s': %s", o.Slot, strings.TrimSpace(r.Stderr))
	}
	tab := jget(r.Stdout, "result.tab.tab_id")
	pane := jget(r.Stdout, "result.root_pane.pane_id")
	if tab == "" || pane == "" {
		return "", fmt.Errorf("herdr tab create returned no tab/pane id for slot '%s'", o.Slot)
	}

	name := AgentName(o.Slot, tab)
	timeoutMs := strconv.Itoa(o.BootTimeout * 1000)
	start := append([]string{"agent", "start", name, "--kind", kind, "--pane", pane, "--timeout", timeoutMs, "--"}, agentArgs...)
	r = h.herdr(ctx, start...)
	if r.ExitCode == 0 {
		return tab, nil
	}
	if jget(r.Stderr, "error.code") != "agent_not_ready" {
		// No agent ever ran in the tab: close it, or it lingers as a shell no
		// slot tracks and no drift check sees (#204).
		h.herdr(ctx, "tab", "close", tab)
		return "", fmt.Errorf("herdr agent start failed for slot '%s' (%s; tab closed): %s", o.Slot, tab, strings.TrimSpace(r.Stderr))
	}
	// Blocked at startup: answer up to two dialogs (trust, then bypass).
	for i := 0; ; {
		text := paneText(h.herdr(ctx, "agent", "read", name, "--source", "visible", "--format", "text"))
		keys, ok := DialogKeys(text)
		if !ok {
			return "", fmt.Errorf("slot '%s' is stuck on an unrecognised startup dialog in tab %s", o.Slot, tab)
		}
		h.herdr(ctx, append([]string{"agent", "send-keys", name}, keys...)...)
		w := h.herdr(ctx, "agent", "wait", name, "--until", "idle", "--until", "blocked", "--timeout", timeoutMs)
		if jget(w.Stdout, "result.agent.agent_status") == "idle" {
			return tab, nil
		}
		if i++; i >= 3 {
			return "", fmt.Errorf("slot '%s' did not reach idle after its startup dialogs (tab %s)", o.Slot, tab)
		}
	}
}

// submitRetries bounds the Enters pressed on a brief that sits unsent.
const submitRetries = 3

// Send submits the file and confirms pickup: it returns once the agent is
// observed working (or blocked on a dialog), NOT when the task finishes,
// since a plain --wait would hold until the whole task settled. ErrNotSubmitted
// means no activity followed the submission; ErrDialogOpen means a dialog was
// already up and nothing was sent.
//
// herdr can type the brief and lose the Enter (#89). A brief left on the
// prompt line is submitted with bounded Enter retries instead of failing.
func (h *herdr) Send(ctx context.Context, slot, handle, file string) error {
	text, err := readFile(file)
	if err != nil {
		return ErrNotSubmitted
	}
	text = strings.TrimRight(text, "\n") // the shell host read it with $(cat)
	name := AgentName(slot, handle)
	r := h.herdr(ctx, "agent", "prompt", name, text,
		"--wait", "--until", "working", "--until", "blocked", "--timeout", "60000")
	if r.ExitCode == 0 {
		return nil
	}
	if jget(r.Stderr, "error.code") == "agent_blocked" {
		return ErrDialogOpen
	}
	if h.briefOnPrompt(ctx, name, text) {
		return h.submitTyped(ctx, name)
	}
	return ErrNotSubmitted
}

// SubmitPending is the resend path: when the file's text is already on the
// prompt line (a prior Send typed it and its Enters were lost), it submits that
// text and reports handled, so the resend never types a second copy on top.
// Not handled means the prompt line holds no such brief and Send applies.
func (h *herdr) SubmitPending(ctx context.Context, slot, handle, file string) (bool, error) {
	text, err := readFile(file)
	if err != nil {
		return false, nil
	}
	name := AgentName(slot, handle)
	if h.Status(ctx, slot, handle) != "idle" || !h.briefOnPrompt(ctx, name, strings.TrimRight(text, "\n")) {
		return false, nil
	}
	return true, h.submitTyped(ctx, name)
}

// pastePlaceholder is how Claude Code shows a long paste on the prompt line.
var pastePlaceholder = regexp.MustCompile(`\[Pasted text #\d+( \+\d+ lines)?\]`)

// briefOnPrompt reports whether the brief sits unsent on the prompt line: the
// text after the pane's last prompt marker holds either the tail of text or a
// paste placeholder. The tail is the brief's last line: it is typed last, so it
// is there only when the whole brief is. Scrollback above the marker is a brief
// already sent and never counts. Whitespace is dropped on both sides so soft
// wraps do not matter.
func (h *herdr) briefOnPrompt(ctx context.Context, name, text string) bool {
	tail := ""
	for _, l := range strings.Split(text, "\n") {
		if t := squeeze(l); t != "" {
			tail = t
		}
	}
	if len(tail) > 80 {
		tail = tail[len(tail)-80:]
	}
	pane := paneText(h.herdr(ctx, "agent", "read", name, "--source", "recent-unwrapped",
		"--lines", "60", "--format", "text"))
	i := strings.LastIndex(pane, promptMarker)
	if i < 0 {
		return false
	}
	prompt := pane[i+len(promptMarker):]
	if pastePlaceholder.MatchString(prompt) {
		return true
	}
	return tail != "" && strings.Contains(squeeze(prompt), tail)
}

const promptMarker = "\u276f"

func squeeze(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// submitTyped presses Enter on a brief already on the prompt line, up to
// submitRetries times, until the agent is working or blocked.
func (h *herdr) submitTyped(ctx context.Context, name string) error {
	for i := 0; i < submitRetries; i++ {
		// Enter only into an idle prompt: on a dialog it would answer the
		// dialog, which nobody approved.
		switch h.statusOf(ctx, name) {
		case "working":
			return nil
		case "blocked":
			return ErrDialogOpen
		case "idle":
		default:
			return ErrNotSubmitted
		}
		h.herdr(ctx, "agent", "send-keys", name, "enter")
		w := h.herdr(ctx, "agent", "wait", name, "--until", "working", "--until", "blocked", "--timeout", "10000")
		switch jget(w.Stdout, "result.agent.agent_status") {
		case "working":
			return nil
		case "blocked":
			return ErrDialogOpen
		}
	}
	return ErrNotSubmitted
}

// Capture prints recent pane text with soft wraps joined (the herdr twin of
// capture-pane -J).
func (h *herdr) Capture(ctx context.Context, slot, handle string, lines int) string {
	if handle == "" {
		return ""
	}
	return paneText(h.herdr(ctx, "agent", "read", AgentName(slot, handle), "--source", "recent-unwrapped",
		"--lines", strconv.Itoa(lines), "--format", "text"))
}

// paneText is the pane text of an `agent read` reply. herdr 0.9.x prints it as
// plain text on stdout, not in the JSON envelope its other commands use; a
// failure is a JSON error on stderr and a non-zero exit, read here as no text.
func paneText(r Result) string {
	if r.ExitCode != 0 {
		return ""
	}
	return r.Stdout
}

// Status is herdr's native agent state, or `gone` when the slot has a tab but
// no agent in it (the session exited back to a shell). A never-dispatched
// slot yields "".
func (h *herdr) Status(ctx context.Context, slot, handle string) string {
	if handle == "" {
		return ""
	}
	return h.statusOf(ctx, AgentName(slot, handle))
}

func (h *herdr) statusOf(ctx context.Context, name string) string {
	r := h.herdr(ctx, "agent", "get", name)
	if r.ExitCode != 0 {
		return "gone"
	}
	return jget(r.Stdout, "result.agent.agent_status")
}

// Kill asks the session to exit, closes its tab, and proves it is gone. It
// records the pane's process PIDs first (claude plus its MCP children) and
// fails unless `tab get` fails and every recorded PID has exited. Closed tab
// ids are never reused, so a failing `tab get` is unambiguous. `/exit` is a
// client-side command, so there is no wait: it produces no turn to observe;
// it lets Claude Code flush its session first.
func (h *herdr) Kill(ctx context.Context, slot, handle string) error {
	if handle == "" {
		return nil
	}
	name := AgentName(slot, handle)
	var pids []int
	if pane := jget(h.herdr(ctx, "agent", "get", name).Stdout, "result.agent.pane_id"); pane != "" {
		pids = processPIDs(h.herdr(ctx, "pane", "process-info", "--pane", pane).Stdout)
	}
	h.herdr(ctx, "agent", "prompt", name, "/exit")
	h.herdr(ctx, "tab", "close", handle)
	alive, ok := killLoop(&h.d, pids, func() bool { return h.herdr(ctx, "tab", "get", handle).ExitCode != 0 })
	if ok {
		return nil
	}
	return fmt.Errorf("slot '%s' previous session is still running (tab %s%s); not spawning a second one",
		slot, handle, pidSuffix(alive))
}

// processPIDs reads foreground process pids and the shell pid out of
// `herdr pane process-info`.
func processPIDs(doc string) []int {
	var v struct {
		Result struct {
			Info struct {
				Fg []struct {
					Pid int `json:"pid"`
				} `json:"foreground_processes"`
				Shell int `json:"shell_pid"`
			} `json:"process_info"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(doc), &v) != nil {
		return nil
	}
	var pids []int
	for _, p := range v.Result.Info.Fg {
		pids = append(pids, p.Pid)
	}
	if v.Result.Info.Shell != 0 {
		pids = append(pids, v.Result.Info.Shell)
	}
	return pids
}

func (h *herdr) Notify(ctx context.Context, title, body string) {
	h.herdr(ctx, "notification", "show", title, "--body", body, "--sound", "request")
}
