package host

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/shlex"
	"github.com/l4ci/rota/internal/strutil"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// herdr is the legacy herdr adapter. A herdr slot is a TAB in the current
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
type herdr struct {
	d Deps
	// noted: the one stderr note a failing label set may write (see Label).
	noted *atomic.Bool
}

func (h *herdr) Kind() Kind { return Herdr }

func (h *herdr) Capabilities() Capabilities { return kindPolicy(Herdr, CapabilitiesOf(h)) }

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
	pane := detectHerdrPane(h.d)
	return pane.managed && pane.workspace
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

// onboardingTheme is Claude Code's first-run theme picker, which a config dir
// with no profile opens on. Enter keeps the highlighted default, which is all
// a worker needs from it.
var onboardingTheme = regexp.MustCompile(`(?m)Choose the text style|^\s*(?:❯|>)?\s*(?:\d+\.\s+)?Dark mode\b[\s\S]*^\s*(?:\d+\.\s+)?Light mode\b`)

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
	if onboardingTheme.MatchString(pane) {
		return []string{"enter"}, true
	}
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

// paneBusyPolls is how many times a second Spawn retries agent start while the
// new tab's pane is not yet an available shell.
const paneBusyPolls = 2

// Spawn adopts the worktree as a new background tab, starts the agent in
// it, clears any startup dialog, and waits for it to idle. It returns the tab
// id (the handle). Session is unused: herdr tabs live in the caller's own
// workspace.
func (h *herdr) Spawn(ctx context.Context, o SpawnOpts) (string, error) {
	kind, env, agentArgs, err := LaunchArgs(o.Launch)
	if err != nil {
		return "", fmt.Errorf("round.dispatch=herdr launches %s itself; the launch command must run one of them, got: %s", harness.KindList(), o.Launch)
	}
	ws := h.d.Getenv("HERDR_WORKSPACE_ID")
	var opened openedWorkspace
	if o.LinkWorkspace {
		switch opened, err = h.openLinked(ctx, h.d.Getenv("HERDR_WORKSPACE_ID"), o.Slot, o.Cwd); {
		case errors.Is(err, ErrUnsupported):
			// A herdr without `worktree open`: the slot is a tab, not a failure.
			fmt.Fprintf(h.d.Stderr, "rota: herdr cannot link workspaces; slot %s stays a tab\n", o.Slot)
			opened = openedWorkspace{}
		case err != nil:
			return "", err
		default:
			ws = opened.id
			if o.Linked != nil {
				*o.Linked = true
			}
		}
	}
	args := []string{"tab", "create", "--workspace", ws,
		"--cwd", o.Cwd, "--label", o.Slot, "--no-focus"}
	for _, e := range env {
		args = append(args, "--env", e)
	}
	// Account selection must happen at tab creation: agent start runs the
	// binary directly and ignores shell aliases or wrappers. The account's
	// variable is already in o.Env (the dispatch asked the kind's harness).
	for _, e := range o.Env {
		args = append(args, "--env", e)
	}
	r := h.herdr(ctx, args...)
	if r.ExitCode != 0 {
		h.dropOpened(ctx, opened)
		return "", fmt.Errorf("herdr tab create failed for slot '%s': %s", o.Slot, strings.TrimSpace(r.Stderr))
	}
	tab := jget(r.Stdout, "result.tab.tab_id")
	pane := jget(r.Stdout, "result.root_pane.pane_id")
	if tab == "" || pane == "" {
		h.dropOpened(ctx, opened)
		return "", fmt.Errorf("herdr tab create returned no tab/pane id for slot '%s'", o.Slot)
	}
	// The root tab `worktree open` made is a plain shell with none of the
	// slot's env: drop it now that the slot's own tab holds the workspace open.
	if opened.rootTab != "" {
		if c := h.herdr(ctx, "tab", "close", opened.rootTab); c.ExitCode != 0 {
			// A plain shell tab left behind keeps the workspace open and
			// shows up as a stray tab in doctor's check.
			h.herdr(ctx, "tab", "close", tab)
			h.dropOpened(ctx, opened)
			return "", fmt.Errorf("herdr tab close failed for slot '%s' root tab %s: %s", o.Slot, opened.rootTab, strings.TrimSpace(c.Stderr))
		}
	}

	name := AgentName(o.Slot, tab)
	timeoutMs := strconv.Itoa(o.BootTimeout * 1000)
	start := append([]string{"agent", "start", name, "--kind", kind, "--pane", pane, "--timeout", timeoutMs, "--"}, agentArgs...)
	r = h.herdr(ctx, start...)
	// A just-created tab's shell may not be ready: agent start answers
	// agent_pane_busy until it is. Poll within the boot budget (#1029).
	for i := 0; i < o.BootTimeout*paneBusyPolls && r.ExitCode != 0 && jget(r.Stderr, "error.code") == "agent_pane_busy"; i++ {
		h.d.Sleep(time.Second / paneBusyPolls)
		r = h.herdr(ctx, start...)
	}
	if r.ExitCode == 0 {
		return tab, nil
	}
	if jget(r.Stderr, "error.code") != "agent_not_ready" {
		// Read the pane before closing it: a failed launch can leave only a
		// shell error, which agent start's timeout does not include (#2533).
		text := paneText(h.herdr(ctx, "pane", "read", pane, "--source", "visible", "--format", "text"))
		detail := startupPaneDetail(text, kind)
		// Close the failed tab, or it lingers as a shell no slot tracks and
		// no drift check sees (#204), even when the diagnostic read failed.
		h.herdr(ctx, "tab", "close", tab)
		return "", fmt.Errorf("herdr agent start failed for slot '%s' (%s; tab closed): %s%s", o.Slot, tab, strings.TrimSpace(r.Stderr), detail)
	}
	// Blocked at startup: answer up to two dialogs (trust, then bypass).
	for i := 0; ; {
		text := paneText(h.herdr(ctx, "agent", "read", name, "--source", "visible", "--format", "text"))
		keys, ok := DialogKeys(text)
		if !ok && looksBooted(text) {
			// herdr did not call the session ready, but the pane is a booted
			// session idle at its empty prompt: nothing is left to answer.
			return tab, nil
		}
		if !ok {
			// The tab is returned with the error: a session is running in it,
			// and the caller records it so the next dispatch reaps it.
			return tab, fmt.Errorf("slot '%s' is stuck on an unrecognised startup dialog in tab %s", o.Slot, tab)
		}
		h.herdr(ctx, append([]string{"agent", "send-keys", name}, keys...)...)
		w := h.herdr(ctx, "agent", "wait", name, "--until", "idle", "--until", "blocked", "--timeout", timeoutMs)
		if jget(w.Stdout, "result.agent.agent_status") == "idle" {
			return tab, nil
		}
		if i++; i >= 3 {
			return tab, fmt.Errorf("slot '%s' did not reach idle after its startup dialogs (tab %s)", o.Slot, tab)
		}
	}
}

// startupPaneDetail keeps the last ten non-empty visible lines, clipping long
// lines so a failed launch does not flood the error. Inspect the full text for
// a missing binary: subsequent shell output may have pushed it out of the tail.
func startupPaneDetail(text, kind string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	for i, line := range lines {
		lines[i] = strutil.Clip(line, 500)
	}
	detail := "\npane output:\n" + strings.Join(lines, "\n")
	binary := regexp.QuoteMeta(kind)
	missing := regexp.MustCompile(`(?m)(?:^|[ \t:])(?:command not found:[ \t]*` + binary + `|` + binary + `:[ \t]*not found)(?:\s|$)`)
	if missing.MatchString(text) {
		detail += fmt.Sprintf("\nmissing binary %s: the pane's shell PATH lacks it (check its default node/environment)", kind)
	}
	return detail
}

// openedWorkspace is the linked workspace a `worktree open` made for a slot.
// rootTab is the shell tab that open created, "" when the workspace was
// already open: that tab is somebody else's and rota must not close it.
type openedWorkspace struct{ id, rootTab string }

// openLinked opens the worktree at path as a workspace linked to primary, so
// herdr groups the slot under its project. open is idempotent
// (`already_open`), and it takes an absolute path only: a relative one fails
// with worktree_not_found. It has no --env, so the caller still creates the
// slot's tab with `tab create --env`.
func (h *herdr) openLinked(ctx context.Context, primary, slot, path string) (openedWorkspace, error) {
	if !strings.HasPrefix(path, "/") {
		return openedWorkspace{}, fmt.Errorf("round.herdrGroupSlots needs an absolute worktree path for slot '%s', got: %s", slot, path)
	}
	r := h.herdr(ctx, "worktree", "open", "--workspace", primary,
		"--path", path, "--label", slot, "--no-focus")
	if r.ExitCode != 0 {
		return openedWorkspace{}, unsupportedOr(r, fmt.Errorf("herdr worktree open failed for slot '%s': %s", slot, strings.TrimSpace(r.Stderr)))
	}
	ws := jget(r.Stdout, "result.workspace.workspace_id")
	if ws == "" {
		return openedWorkspace{}, fmt.Errorf("herdr worktree open returned no workspace id for slot '%s'", slot)
	}
	w := openedWorkspace{id: ws}
	if jget(r.Stdout, "result.already_open") != "true" {
		w.rootTab = jget(r.Stdout, "result.tab.tab_id")
	}
	return w, nil
}

// dropOpened closes a workspace this Spawn opened and never got a tab into, so
// a failed dispatch leaves no empty workspace behind. A workspace that was
// already open is left alone.
func (h *herdr) dropOpened(ctx context.Context, w openedWorkspace) {
	if w.rootTab == "" {
		return
	}
	if r := h.herdr(ctx, "workspace", "close", w.id); r.ExitCode != 0 {
		fmt.Fprintf(h.d.Stderr, "rota: herdr workspace %s was left open after a failed dispatch: %s\n", w.id, strings.TrimSpace(r.Stderr))
	}
}

// Unverifiable is the prefix of a Status that is no answer: the host could not
// say whether the agent lives. The reason follows it.
const (
	UnverifiablePrefix    = "unverifiable:"
	ReasonHostUnreachable = "host-unreachable"
	ReasonStaleHandle     = "stale-handle"
	ReasonPaneUnreadable  = "pane-unreadable"
)

// Notify raises a herdr notification. herdr answers with why it did or did not
// show it; anything but `shown` (and `disabled`, the user's own choice) means a
// blocked or needs-permission alert never reached a human, so it is reported
// once on stderr. No retry.
func (h *herdr) Notify(ctx context.Context, title, body string) {
	r := h.herdr(ctx, "notification", "show", title, "--body", body, "--sound", "request")
	reason := jget(r.Stdout, "result.reason")
	if r.ExitCode != 0 {
		reason = "herdr error"
	}
	switch reason {
	case "", "shown", "disabled":
		return
	}
	fmt.Fprintf(h.d.Stderr, "rota: herdr notification %q was not shown (%s)\n", title, reason)
}

// unsupportedOr wraps err in ErrUnsupported when the failed command r says herdr
// lacks the method or a flag (herdr's unknown_method, or a clap usage error).
// Probing the failure, not the version, is what the no-pin decision asks for.
func unsupportedOr(r Result, err error) error {
	low := strings.ToLower(r.Stderr)
	if jget(r.Stderr, "error.code") == "unknown_method" ||
		strings.Contains(low, "unexpected argument") || strings.Contains(low, "unknown flag") || strings.Contains(low, "unrecognized") {
		return fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	return err
}
