package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/keepalive"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/status"
	"github.com/l4ci/rota/internal/worker"
)

// D1 (#65): `rota statusline dump` and the `rota hook` verbs. The logic is
// internal/hook; this file is the process side: stdin, the environment, the
// lease and the settings files.

func statuslineCommands() *Command {
	return &Command{Name: "statusline", Summary: "statusline command that records session state for the orchestrator hooks", Subs: []*Command{
		{Name: "dump", Summary: "record the session state from stdin, then run --then with the same input", Verb: statuslineDump},
	}}
}

func hookCommands() *Command {
	return &Command{Name: "hook", Summary: "Claude Code hooks that hand the orchestrator off before its context runs out", Subs: []*Command{
		{Name: "stop", Summary: "Stop hook: block above the context threshold until a handoff is written, or while workers run with no watch armed", Verb: noFlags(hookStop)},
		{Name: "prompt", Summary: "UserPromptSubmit hook: one-line round digest and a missing-watch reminder", Verb: noFlags(hookPrompt)},
		{Name: "session-start", Summary: "SessionStart hook: inject and consume the handoff", Verb: noFlags(hookSessionStart)},
		{Name: "install", Summary: "merge the hooks and the statusline into a Claude Code settings file", Verb: hookInstall},
		{Name: "uninstall", Summary: "remove what install wrote and restore a wrapped statusline", Verb: hookUninstall},
	}}
}

const maxHookInput = 8 << 20

// hookNow is the clock; ROTA_TEST_NOW (RFC 3339) fixes it for tests.
func hookNow() time.Time {
	if v := os.Getenv("ROTA_TEST_NOW"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
	}
	return time.Now()
}

// hookHolderPID is a test seam: ROTA_TEST_HOLDER_PID stands in for the nearest
// non-shell ancestor, which a smoke test cannot arrange.
func hookHolderPID() int {
	n, _ := strconv.Atoi(os.Getenv("ROTA_TEST_HOLDER_PID"))
	return n
}

func readInput(c *Ctx) []byte {
	b, _ := io.ReadAll(io.LimitReader(c.Stdin, maxHookInput))
	return b
}

// payloadCwd is the cwd a hook or statusline payload names, else the process
// working directory.
func payloadCwd(m map[string]any) string {
	if s, _ := m["cwd"].(string); s != "" {
		return s
	}
	if ws, _ := m["workspace"].(map[string]any); ws != nil {
		if s, _ := ws["current_dir"].(string); s != "" {
			return s
		}
	}
	d, _ := os.Getwd()
	return d
}

func statuslineDump(fs *flag.FlagSet) RunFunc {
	then := fs.String("then", "", "shell command to run with the same input (the statusline being wrapped)")
	return func(c *Ctx, args []string) (Result, error) {
		if c.JSON {
			c.JSON = false // stdout belongs to the wrapped command: no envelope, not even for this error
			return Result{}, Usage("statusline dump does not take --json: stdout belongs to the wrapped command")
		}
		debug := os.Getenv("ROTA_STATUSLINE_DEBUG") != ""
		input := readInput(c)
		if err := dumpState(input); err != nil && debug {
			fmt.Fprintf(c.Stderr, "rota statusline dump: %v\n", err)
		}
		if *then != "" {
			cmd := exec.CommandContext(c.Context(), "sh", "-c", *then)
			cmd.Stdin = bytes.NewReader(input)
			cmd.Stdout, cmd.Stderr = c.Stdout, c.Stderr
			if err := cmd.Run(); err != nil && debug {
				fmt.Fprintf(c.Stderr, "rota statusline dump: --then: %v\n", err)
			}
		}
		return Result{}, nil
	}
}

func dumpState(input []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	var m map[string]any
	if json.Unmarshal(input, &m) != nil || m == nil {
		return fmt.Errorf("input is not a JSON object")
	}
	cd, err := roundlease.CommonDir(payloadCwd(m))
	if err != nil {
		return err
	}
	return hook.Dump(cd, input, hookNow())
}

// hookPrint is a hook's stdout: one JSON line, no HTML escaping.
func hookPrint(v any) Result {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(v) != nil {
		return Result{}
	}
	return Result{Text: strings.TrimSuffix(b.String(), "\n")}
}

// hookContext is what both hooks need: who the session is relative to the
// lease, the config of the lease's project and the handoff path.
type hookContext struct {
	payload   map[string]any
	commonDir string
	who       hook.Who
	root      string
	cfg       any
	set       hook.Settings
	handoff   string // absolute path
}

// hookSetup returns false for every reason the hook should pass silently.
// needLeaseFree additionally accepts a session with no live lease (the
// SessionStart fallback), using the project root found from its cwd.
func hookSetup(c *Ctx, needLeaseFree bool) (hc hookContext, ok bool) {
	if json.Unmarshal(readInput(c), &hc.payload) != nil || hc.payload == nil {
		return hc, false
	}
	cwd := payloadCwd(hc.payload)
	cd, err := roundlease.CommonDir(cwd)
	if err != nil {
		return hc, false
	}
	hc.commonDir = cd
	hc.who = hook.Identify(roundlease.DefaultEnv(), os.Getenv, hookHolderPID(), cd)
	switch {
	case hc.who.Orchestrator:
		hc.root = hc.who.Lease.Root
	case needLeaseFree && hc.who.LeaseFree:
		hc.root = findHvRoot(cwd)
	}
	if hc.root == "" {
		return hc, false
	}
	hc.cfg = config.Load(filepath.Join(hc.root, ".rota", "config.json"))
	if hc.set, err = hook.LoadSettings(hc.cfg); err != nil {
		return hc, false // a bad value is a pass, never a block
	}
	hc.handoff = handoffFile(hc.root, hc.cfg)
	return hc, true
}

func findHvRoot(dir string) string {
	for d := dir; d != ""; {
		if fi, err := os.Stat(filepath.Join(d, ".rota")); err == nil && fi.IsDir() {
			return d
		}
		p := filepath.Dir(d)
		if p == d {
			break
		}
		d = p
	}
	return ""
}

// handoffFile is the absolute `.rota/handoff/<base>.md`, the path
// `rota status handoff <base> --canonical` returns.
func handoffFile(root string, cfg any) string {
	configured := ""
	if v, err := config.Value(cfg, "git.baseBranch"); err == nil {
		configured, _ = v.(string)
	}
	base, ok, _ := git.Repo{Dir: root}.Base(context.Background(), configured)
	if !ok || base == "" {
		base = configured
		if base == "" {
			base = "main"
		}
	}
	rel, err := status.HandoffPath(base, "")
	if err != nil {
		rel = ".rota/handoff/main.md"
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

func hookStop(c *Ctx, args []string) (res Result, _ error) {
	c.JSON = false
	defer func() {
		if r := recover(); r != nil {
			res = Result{}
		}
	}()
	hc, ok := hookSetup(c, false)
	if !ok {
		return Result{}, nil
	}
	sid, _ := hc.payload["session_id"].(string)
	active, _ := hc.payload["stop_hook_active"].(bool)
	if d, ok := stopHandoff(hc, sid, active); ok && d.Block {
		return hookPrint(hook.StopOutput(d.Reason)), nil
	}
	// A second stop in the same turn passes, so a refused watch cannot loop.
	if !active {
		if reason, block := watchBlock(hc); block {
			return hookPrint(hook.StopOutput(reason)), nil
		}
	}
	return Result{}, nil
}

// stopHandoff is the context-threshold decision (D1, D4). ok is false when the
// session has no readable state, so there is no decision to take.
func stopHandoff(hc hookContext, sid string, active bool) (d hook.StopDecision, ok bool) {
	path, err := hook.StatePath(hc.commonDir, sid)
	if err != nil {
		return d, false
	}
	st, found, err := hook.ReadState(path)
	if err != nil || !found {
		return d, false
	}
	now := hookNow()
	in := hook.StopIn{StopHookActive: active}
	if hc.set.SwitchOnUsage {
		in.Supervised, in.HoldUntil = supervisedHold(hc.commonDir, hookNow())
	}
	d = hook.DecideStop(in, st, hc.set, hook.StatHandoff(hc.handoff), hc.handoff, now)
	if d.Persist {
		// Only the counters: a statusline refresh may have landed meanwhile.
		_ = hook.UpdateState(path, func(cur hook.State, found bool) hook.State {
			if !found {
				cur = st
			}
			cur.HandoffBlocks, cur.BlockedAt, cur.HandoffFailed = d.State.HandoffBlocks, d.State.BlockedAt, d.State.HandoffFailed
			cur.UsageHandoff = d.State.UsageHandoff
			return cur
		})
	}
	return d, true
}

// watchBlock is the #81 rule: an orchestrator that holds the lease, has
// workers to wait for and no `rota round watch` running must not go idle,
// because nothing would wake it when a worker finishes. A solo round has no
// panes to watch.
func watchBlock(hc hookContext) (reason string, block bool) {
	if worker.RegistryHost(hc.root) == host.Solo {
		return "", false
	}
	need, attn := roundwatch.NeedsWatch(hc.root)
	if !need {
		return "", false
	}
	if _, armed := roundwatch.Armed(roundlease.DefaultEnv(), hc.commonDir); armed {
		return "", false
	}
	reason = "No `rota round watch` is running, and workers are active: nothing would wake you when one finishes. " +
		"Start `rota round watch` as a background command now (one at a time; it exits with JSON when a slot, PR or escalation changes, or at its heartbeat), then stop."
	if len(attn) > 0 {
		reason += " Waiting on you already: " + strings.Join(attn, ", ") + "."
	}
	return reason, true
}

// hookPrompt is the UserPromptSubmit hook: a one-line round digest on every
// maintainer message, so an answer always comes with the slot state and a
// reminder when no watch is armed. Silent for anyone but the orchestrator and
// when nothing is active.
func hookPrompt(c *Ctx, args []string) (res Result, _ error) {
	c.JSON = false
	defer func() {
		if r := recover(); r != nil {
			res = Result{}
		}
	}()
	hc, ok := hookSetup(c, false)
	if !ok {
		return Result{}, nil
	}
	if worker.RegistryHost(hc.root) == host.Solo {
		return Result{}, nil
	}
	if need, _ := roundwatch.NeedsWatch(hc.root); !need {
		return Result{}, nil
	}
	_, armed := roundwatch.Armed(roundlease.DefaultEnv(), hc.commonDir)
	out := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     "UserPromptSubmit",
		"additionalContext": roundwatch.Digest(hc.root, hc.who.Lease.Round, armed),
	}}
	return hookPrint(out), nil
}

// supervisedHold reads keepalive.json the way `rota keepalive status` does: a
// supervisor is live when the file says running and its pid is alive. The
// hold is its switchHold when that is still ahead (D4).
func supervisedHold(commonDir string, now time.Time) (supervised bool, until time.Time) {
	ks, found, err := keepalive.ReadState(keepalive.StatePath(commonDir))
	if err != nil || !found || ks.Status != keepalive.StatusRunning || !roundlease.DefaultEnv().Alive(ks.PID) {
		return false, time.Time{}
	}
	if ks.SwitchHold != nil {
		if t, err := time.Parse(time.RFC3339, ks.SwitchHold.Until); err == nil && t.After(now) {
			until = t
		}
	}
	return true, until
}

func hookSessionStart(c *Ctx, args []string) (res Result, _ error) {
	c.JSON = false
	defer func() {
		if r := recover(); r != nil {
			res = Result{}
		}
	}()
	hc, ok := hookSetup(c, true)
	if !ok {
		return Result{}, nil
	}
	source, _ := hc.payload["source"].(string)
	ho := hook.StatHandoff(hc.handoff)
	if !ho.Exists {
		return Result{}, nil
	}
	maxAge := time.Duration(hc.set.HandoffMaxAge) * time.Second
	if !hook.ShouldInject(source, hc.who.Orchestrator, hc.who.LeaseFree, ho, hook.FirstLine(hc.handoff), maxAge, hookNow()) {
		return Result{}, nil
	}
	body, ok := hook.Consume(hc.handoff)
	if !ok {
		return Result{}, nil
	}
	out := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     "SessionStart",
		"additionalContext": hook.InjectPrefix + "\n\n" + body,
	}}
	return hookPrint(out), nil
}

// settingsPaths maps each scope to its file. Project scopes are "" outside a
// project root.
func settingsPaths(root string) map[hook.Scope]string {
	cfgDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if cfgDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cfgDir = filepath.Join(home, ".claude")
		}
	}
	m := map[hook.Scope]string{}
	if cfgDir != "" {
		m[hook.ScopeUser] = filepath.Join(cfgDir, "settings.json")
	}
	if root != "" {
		m[hook.ScopeProjectLocal] = filepath.Join(root, ".claude", "settings.local.json")
		m[hook.ScopeProject] = filepath.Join(root, ".claude", "settings.json")
	}
	return m
}

// settingsTarget resolves --scope to its file, with the exit codes the
// contract names: 3 when a project scope is asked outside a project root.
func settingsTarget(c *Ctx, scopeFlag string) (hook.Scope, string, map[hook.Scope]string, error) {
	scope := hook.Scope(scopeFlag)
	if !scope.Valid() {
		return "", "", nil, Usage("--scope must be project-local, project or user")
	}
	root, rerr := c.Root()
	if rerr != nil && scope != hook.ScopeUser {
		return "", "", nil, rerr
	}
	paths := settingsPaths(root)
	p := paths[scope]
	if p == "" {
		return "", "", nil, Resolution("no settings path for scope %s (CLAUDE_CONFIG_DIR and HOME unset)", scope)
	}
	return scope, p, paths, nil
}

func writeSettings(path string, o *jsonx.Object) error {
	b, err := jsonx.Marshal(o)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	return fsio.WriteFileAtomic(path, append(b, '\n'))
}

func hookInstall(fs *flag.FlagSet) RunFunc {
	scopeFlag := fs.String("scope", "project-local", "project-local, project or user")
	wrap := fs.Bool("wrap-statusline", false, "wrap an existing statusline instead of refusing")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		scope, path, paths, err := settingsTarget(c, *scopeFlag)
		if err != nil {
			return Result{}, err
		}
		files := map[hook.Scope]*jsonx.Object{}
		for sc, p := range paths {
			o, rerr := hook.ReadSettings(p)
			if rerr != nil {
				if sc == scope {
					return Result{}, &Error{Exit: ExitInternal, Message: rerr.Error(), Hint: "fix or move the settings file; rota will not overwrite a file it cannot parse"}
				}
				o = nil // another scope's broken file does not block this one
			}
			files[sc] = o
		}
		if files[scope] == nil {
			files[scope] = jsonx.NewObject()
		}
		out, err := hook.Install(hook.InstallIn{Scope: scope, Wrap: *wrap, Files: files})
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		if out.Blocked {
			d := knObj("blockedBy", "statusline exists", "scope", string(scope), "settingsPath", path, "changed", false)
			return Result{Data: d}, Refused("a statusline is already configured and would be replaced").
				WithHint("run: rota hook install --wrap-statusline (keeps it, runs it after the dump)")
		}
		if out.Changed {
			if err := writeSettings(path, files[scope]); err != nil {
				return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
			}
		}
		d := knObj("scope", string(scope), "settingsPath", path, "hooks", out.Hooks, "statusline", out.Statusline, "changed", out.Changed)
		text := fmt.Sprintf("%s: hooks %s, statusline %s", path, strings.Join(out.Hooks, ", "), out.Statusline)
		if !out.Changed {
			text = path + ": already installed"
		}
		return Result{Data: d, Text: text}, nil
	}
}

func hookUninstall(fs *flag.FlagSet) RunFunc {
	scopeFlag := fs.String("scope", "project-local", "project-local, project or user")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		scope, path, _, err := settingsTarget(c, *scopeFlag)
		if err != nil {
			return Result{}, err
		}
		o, rerr := hook.ReadSettings(path)
		if rerr != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: rerr.Error()}
		}
		removed := hook.Uninstall(o)
		if removed == nil {
			removed = []string{}
		}
		if len(removed) > 0 {
			if err := writeSettings(path, o); err != nil {
				return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
			}
		}
		d := knObj("scope", string(scope), "settingsPath", path, "removed", removed, "changed", len(removed) > 0)
		text := path + ": nothing to remove"
		if len(removed) > 0 {
			text = path + ": removed " + strings.Join(removed, ", ")
		}
		return Result{Data: d, Text: text}, nil
	}
}
