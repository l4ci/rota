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
	"strings"
	"time"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/bugreport"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/keepalive"
	"github.com/l4ci/rota/internal/knowledge"
	"github.com/l4ci/rota/internal/landfreeze"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/skills"
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
		{Name: "guard", Summary: "PreToolUse hook: recall path-scoped context before Edit/Write; deny destructive git for workers", Verb: noFlags(hookGuard)},
		{Name: "session-start", Summary: "SessionStart hook: inject and consume the handoff", Verb: noFlags(hookSessionStart)},
		{Name: "install", Summary: "merge the hooks and the statusline into a Claude Code settings file", Verb: hookInstall},
		{Name: "uninstall", Summary: "remove what install wrote and restore a wrapped statusline", Verb: hookUninstall},
	}}
}

const maxHookInput = 8 << 20

func readInput(c *Ctx) []byte {
	b, _ := io.ReadAll(io.LimitReader(c.Stdin, maxHookInput))
	return b
}

func statuslineDump(fs *flag.FlagSet) RunFunc {
	then := fs.String("then", "", "shell command to run with the same input (the statusline being wrapped)")
	return func(c *Ctx, args []string) (Result, error) {
		if c.JSON {
			c.JSON = false // stdout belongs to the wrapped command: no envelope, not even for this error
			return Result{}, Usage("statusline dump does not take --json: stdout belongs to the wrapped command")
		}
		debug := c.deps().Getenv("ROTA_STATUSLINE_DEBUG") != ""
		input := readInput(c)
		if err := dumpState(c, input, c.deps().Now()); err != nil && debug {
			fmt.Fprintf(c.Stderr, "rota statusline dump: %v\n", err)
		}
		if *then != "" {
			// Raw exec on purpose: the statusline runs on every redraw and
			// proc.Run's buffering and 5s WaitDelay would stall it when the
			// wrapped script backgrounds a job.
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

func dumpState(c *Ctx, input []byte, now time.Time) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	p, err := hook.ParsePayload(input)
	if err != nil {
		return err
	}
	cwd := p.Cwd
	if cwd == "" {
		cwd, _ = c.deps().Getwd()
	}
	cd, err := rotastate.CommonDirVia(c.Context(), c.deps().Git, cwd)
	if err != nil {
		return err
	}
	return hook.Dump(cd, p, now)
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
	payload   hook.Payload
	commonDir string
	who       hookWho
	root      string
	cfg       any
	set       hook.Settings
	handoff   string // absolute path
	now       func() time.Time
	lease     roundlease.Env
}

// hookSetup returns false for every reason the hook should pass silently.
// needLeaseFree additionally accepts a session with no live lease (the
// SessionStart fallback), using the project root found from its cwd.
func hookSetup(c *Ctx, needLeaseFree bool) (hc hookContext, ok bool) {
	return hookSetupFor(c, needLeaseFree, false)
}

// hookSetupFor is hookSetup; anySession additionally accepts a session that is
// not the orchestrator (the context guard), using the project root found from
// its cwd. The caller tells the two apart by hc.who.Orchestrator.
func hookSetupFor(c *Ctx, needLeaseFree, anySession bool) (hc hookContext, ok bool) {
	var err error
	if hc.payload, err = hook.ParsePayload(readInput(c)); err != nil {
		return hc, false
	}
	wd, _ := c.deps().Getwd()
	cwd := hc.payload.CwdOr(wd)
	cd, err := rotastate.CommonDirVia(c.Context(), c.deps().Git, cwd)
	if err != nil {
		return hc, false
	}
	hc.commonDir = cd
	hc.now = c.deps().Now
	hc.lease = c.deps().LeaseEnv()
	hc.who = hookIdentify(hc.lease, c.deps().HolderPID(), c.deps().Getenv, cd)
	switch {
	case hc.who.Orchestrator:
		hc.root = hc.who.Lease.Root
	case anySession || (needLeaseFree && hc.who.LeaseFree):
		hc.root, _ = rotatree.Find(cwd)
	}
	if hc.root == "" {
		return hc, false
	}
	hc.cfg = c.deps().Config(hc.root)
	if hc.set, err = hook.LoadSettings(hc.cfg); err != nil {
		return hc, false // a bad value is a pass, never a block
	}
	switch {
	case hc.who.Orchestrator || !anySession:
		hc.handoff = handoffFile(c.deps(), hc.root, hc.cfg)
	case config.ContextGuard(hc.cfg) == hook.GuardAuto:
		// A work session pauses to .rota/handoff/<its branch>.md, where
		// /rota-pause writes it; only the auto guard reads it.
		hc.handoff = branchHandoffFile(c.deps(), hc.root, cwd)
	}
	return hc, true
}

// branchHandoffFile is the absolute `.rota/handoff/<branch>.md` of the branch
// checked out in cwd, the path `rota status handoff <branch> --canonical`
// returns from there. "" on a detached HEAD, so no note counts.
func branchHandoffFile(d *Deps, root, cwd string) string {
	branch, err := d.Repo(cwd).CurrentBranch(context.Background())
	if err != nil || branch == "" {
		return ""
	}
	return hook.NotePath(root, branch)
}

// noHandoffBaseMsg says why handoffFile found no path: the named key when
// git.baseBranch is set but missing, else the shared candidate list.
func noHandoffBaseMsg(cfg any) string {
	if v, err := config.Value(cfg, "git.baseBranch"); err == nil {
		if configured, _ := v.(string); configured != "" {
			return fmt.Sprintf("git.baseBranch %q does not resolve to a branch", configured)
		}
	}
	return noBaseMsg
}

// handoffFile is the absolute `.rota/handoff/<base>.md`, the path
// `rota status handoff <base> --canonical` returns. It is "" when no base
// resolves, so a caller reads no note rather than one filed under a guessed
// branch.
func handoffFile(d *Deps, root string, cfg any) string {
	configured := ""
	if v, err := config.Value(cfg, "git.baseBranch"); err == nil {
		configured, _ = v.(string)
	}
	base, ok, _ := d.Repo(root).Base(context.Background(), configured)
	if !ok {
		return ""
	}
	return hook.NotePath(root, base)
}

func hookStop(c *Ctx, args []string) (res Result, _ error) {
	c.JSON = false
	defer func() {
		if r := recover(); r != nil {
			res = Result{}
		}
	}()
	hc, ok := hookSetupFor(c, false, true)
	if !ok {
		return Result{}, nil
	}
	sid := hc.payload.SessionID
	active, _ := hc.payload.Fields["stop_hook_active"].(bool)
	if !hc.who.Orchestrator {
		if d, ok := guardStop(hc, sid); ok && d.Block {
			return hookPrint(hook.StopOutput(d.Reason)), nil
		}
		if !active {
			if msg := bugreportStopOffer(c, hc.root, sid); msg != "" {
				return hookPrint(map[string]string{"systemMessage": msg}), nil
			}
		}
		return Result{}, nil
	}
	if d, ok := stopHandoff(hc, sid, active); ok && d.Block {
		return hookPrint(hook.StopOutput(d.Reason)), nil
	}
	// A second stop in the same turn passes, so a refused watch cannot loop.
	if !active {
		if reason, block := roundwatch.StopBlock(hc.lease, hc.root, hc.commonDir); block {
			return hookPrint(hook.StopOutput(reason)), nil
		}
	}
	return Result{}, nil
}

// stopHandoff applies the orchestrator's context/usage handoff rule.
func stopHandoff(hc hookContext, sid string, active bool) (hook.SessionDecision, bool) {
	in := hook.StopIn{StopHookActive: active}
	if hc.set.SwitchOnUsage {
		in.Supervised, in.HoldUntil = keepalive.SupervisedHold(hc.lease, hc.commonDir, hc.now())
	}
	return hook.ApplySession(hook.SessionIn{Action: hook.StopHandoff, CommonDir: hc.commonDir, SessionID: sid,
		Settings: hc.set, HandoffPath: hc.handoff, Stop: in, Now: hc.now})
}

// guardStop applies the auto context guard to a non-orchestrator session.
func guardStop(hc hookContext, sid string) (hook.SessionDecision, bool) {
	if config.ContextGuard(hc.cfg) != hook.GuardAuto {
		return hook.SessionDecision{}, false
	}
	return hook.ApplySession(hook.SessionIn{Action: hook.GuardStop, CommonDir: hc.commonDir, SessionID: sid,
		Settings: hc.set, HandoffPath: hc.handoff, Now: hc.now})
}

// guardPrompt applies the warn context guard to a non-orchestrator session.
func guardPrompt(hc hookContext, sid string) string {
	if config.ContextGuard(hc.cfg) != hook.GuardWarn {
		return ""
	}
	d, _ := hook.ApplySession(hook.SessionIn{Action: hook.GuardPrompt, CommonDir: hc.commonDir, SessionID: sid,
		Settings: hc.set, Now: hc.now})
	return d.Line
}

// hookPrompt is the UserPromptSubmit hook: a one-line round digest on every
// maintainer message, so an answer always comes with the slot state and a
// reminder when no watch is armed. Any other session gets only the context
// guard line (work.contextGuard=warn); the orchestrator is silent when nothing
// is active.
func hookPrompt(c *Ctx, args []string) (res Result, _ error) {
	c.JSON = false
	defer func() {
		if r := recover(); r != nil {
			res = Result{}
		}
	}()
	hc, ok := hookSetupFor(c, false, true)
	if !ok {
		return Result{}, nil
	}
	if !hc.who.Orchestrator {
		sid := hc.payload.SessionID
		line := guardPrompt(hc, sid)
		if line == "" {
			return Result{}, nil
		}
		return hookPrint(map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": line,
		}}), nil
	}
	activeFreeze := landfreeze.Frozen(c.Context(), c.deps().Git, hc.root)
	marker, armed := roundwatch.Armed(hc.lease, hc.commonDir)
	interval := roundwatch.DefaultHeartbeat
	if armed && marker.Heartbeat > 0 {
		interval = time.Duration(marker.Heartbeat) * time.Second
	}
	waitLine := roundwatch.MaintainerSnapshotLine(hc.commonDir, c.deps().Now(), interval)
	if worker.IsSolo(hc.root) && !activeFreeze && waitLine == "" {
		return Result{}, nil
	}
	if need, _ := roundwatch.NeedsWatch(hc.root); !need && !activeFreeze && waitLine == "" {
		return Result{}, nil
	}
	digest := roundwatch.Digest(c.Context(), c.deps().Git, hc.root, hc.who.Lease.Round, armed)
	if waitLine != "" {
		digest += " " + waitLine + "."
	}
	out := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     "UserPromptSubmit",
		"additionalContext": digest,
	}}
	return hookPrint(out), nil
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
	source := hc.payload.String("source")
	note, err := hook.ReadNote(hc.handoff)
	if err != nil || !note.Exists {
		return Result{}, nil
	}
	maxAge := time.Duration(hc.set.HandoffMaxAge) * time.Second
	if !hook.ShouldInject(source, hc.who.Orchestrator, hc.who.LeaseFree, note, maxAge, hc.now()) {
		return Result{}, nil
	}
	body, err := note.Consume()
	if err != nil {
		return Result{}, nil
	}
	out := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     "SessionStart",
		"additionalContext": hook.InjectPrefix + "\n\n" + body,
	}}
	return hookPrint(out), nil
}

// hookGuard recalls path-scoped context before Edit/Write for any session.
// For Bash it exits 2 when a worker runs a command its contract forbids (#702).
// Unreadable payloads pass silently.
func hookGuard(c *Ctx, args []string) (res Result, _ error) {
	c.JSON = false
	defer func() {
		if r := recover(); r != nil {
			res = Result{}
		}
	}()
	payload, err := hook.ParsePayload(readInput(c))
	if err != nil {
		return Result{}, nil
	}
	wd, _ := c.deps().Getwd()
	if tool := payload.String("tool_name"); tool == "Edit" || tool == "Write" {
		cwd := payload.CwdOr(wd)
		file := payload.String("tool_input", "file_path")
		root, found := rotatree.Find(cwd)
		if !found || file == "" {
			return Result{}, nil
		}
		if !filepath.IsAbs(file) {
			file = filepath.Join(cwd, file)
		}
		st := knowledge.Store{Root: root, Repos: repos.Paths(root), Config: c.deps().Config}
		entries, err := st.PathEntries(file)
		if err != nil || len(entries) == 0 {
			return Result{}, nil
		}
		cd, err := rotastate.CommonDirVia(c.Context(), c.deps().Git, cwd)
		if err != nil {
			return Result{}, nil
		}
		context := hook.Recall(cd, payload.SessionID, entries, c.deps().Now())
		if context == "" {
			return Result{}, nil
		}
		return hookPrint(map[string]any{"hookSpecificOutput": map[string]any{
			"hookEventName": "PreToolUse", "additionalContext": context,
		}}), nil
	}
	slot := c.deps().Getenv("ROTA_SLOT")
	if slot == "" {
		return Result{}, nil
	}
	gitOut := func(dir string, a ...string) (string, error) {
		r, err := c.deps().Git(c.Context(), dir, a...)
		if err != nil {
			return "", err
		}
		if r.ExitCode != 0 {
			return "", fmt.Errorf("git %s: exit %d", strings.Join(a, " "), r.ExitCode)
		}
		return strings.TrimSpace(r.Stdout), nil
	}
	if reason := hook.DecideGuard(hook.GuardRequest{Payload: payload, Slot: slot, DefaultCwd: wd, Git: gitOut}); reason != "" {
		return Result{}, &Error{Exit: ExitUsage, Message: "rota guard: " + reason}
	}
	return Result{}, nil
}

// settingsPaths maps each scope to its file. Project scopes are "" outside a
// project root.
func settingsPaths(root, home string, getenv func(string) string) map[hook.Scope]string {
	cfgDir := skills.ClaudeDir(getenv, home)
	m := map[hook.Scope]string{}
	for _, sc := range hook.Scopes {
		base := root
		if sc == hook.ScopeUser {
			base = cfgDir
		}
		if p := hook.SettingsPath(sc, base); p != "" {
			m[sc] = p
		}
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
	home, _ := c.deps().UserHomeDir()
	paths := settingsPaths(root, home, c.deps().Getenv)
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
			d := knObj("blockedBy", blockedby.StatuslineExists, "scope", string(scope), "settingsPath", path, "changed", false)
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
		if !guardReachesWorkers(files) {
			c.Warn("the worker guard (PreToolUse) sits only in project-local scope, which worker worktrees do not read: run rota hook install --scope project (or --scope user)")
		}
		return Result{Data: d, Text: text}, nil
	}
}

// guardReachesWorkers reports whether a marked PreToolUse entry sits in a scope
// a worker worktree reads: project or user. Project-local is untracked.
func guardReachesWorkers(files map[hook.Scope]*jsonx.Object) bool {
	for _, sc := range []hook.Scope{hook.ScopeProject, hook.ScopeUser} {
		if _, ok := hook.MarkedEvents(files[sc])[hook.EventGuard]; ok {
			return true
		}
	}
	return false
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

// bugreportStopOffer is the solo session's end-of-turn offer (#1175): one
// non-blocking line, at most once per session, naming the open records. An
// unanswered record is offered again by the next session. A round worker stays
// silent (wind-down offers for the round).
func bugreportStopOffer(c *Ctx, root, sid string) string {
	if c.deps().Getenv("ROTA_SLOT") != "" {
		return ""
	}
	if sid == "" {
		sid = "-"
	}
	open, err := bugreport.Open(c.Context(), c.deps().Git, root)
	if err != nil {
		return ""
	}
	var fresh []bugreport.Record
	for _, r := range open {
		if r.OfferedSession != sid {
			fresh = append(fresh, r)
		}
	}
	if len(fresh) == 0 {
		return ""
	}
	for _, r := range fresh {
		_, _ = bugreport.Update(c.Context(), c.deps().Git, root, r.ID, func(r *bugreport.Record) { r.OfferedSession = sid })
	}
	return bugreportOffer(fresh)
}
