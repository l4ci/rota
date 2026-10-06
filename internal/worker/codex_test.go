package worker

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
)

// codexHelp is the `codex --help` the rig prints: every flag the default
// launch line uses.
const codexHelp = `Usage: codex [OPTIONS]

Options:
  -m, --model <MODEL>
      --dangerously-bypass-approvals-and-sandbox
      --dangerously-bypass-hook-trust
      --no-daemon
      --no-alt-screen
`

// ── preflight ───────────────────────────────────────────────────────────────

// codexRig scripts codex and herdr: every call is logged with its env, and no
// real binary is reachable (Env.Run and Env.LookPath replace exec).
type codexRig struct {
	calls     []string
	version   string // `codex --version` stdout; "" means codex-cli 0.159.2
	noFlag    string // a launch flag `codex --help` leaves out
	loggedIn  bool
	installed bool // herdr reports codex current
	installRC int
	missing   map[string]bool
}

func (r *codexRig) env(h host.Host) Env {
	e := envWith(h)
	e.Executable = func() (string, error) { return "/opt/rota", nil }
	e.LookPath = func(n string) (string, error) {
		if r.missing[n] {
			return "", errors.New("not found")
		}
		return "/fake/" + n, nil
	}
	e.Run = func(_ context.Context, name string, args, env []string) (host.Result, error) {
		call := strings.TrimPrefix(name, "/fake/") + " " + strings.Join(args, " ")
		r.calls = append(r.calls, call+" | "+strings.Join(env, " "))
		switch call {
		case "codex --version":
			v := r.version
			if v == "" {
				v = "codex-cli 0.159.2\n"
			}
			return host.Result{Stdout: v}, nil
		case "codex --help":
			return host.Result{Stdout: strings.ReplaceAll(codexHelp, r.noFlag+"\n", "\n")}, nil
		case "codex login status":
			if r.loggedIn {
				return host.Result{Stdout: "Logged in\n"}, nil
			}
			return host.Result{Stdout: "Not logged in\n", ExitCode: 1}, nil
		case "herdr integration status":
			if r.installed {
				return host.Result{Stdout: "claude: current (v10)\ncodex: current (v8) (/x)\n"}, nil
			}
			return host.Result{Stdout: "claude: current (v10)\ncodex: not installed (/x)\n"}, nil
		case "herdr integration install codex":
			r.installed = r.installRC == 0
			return host.Result{ExitCode: r.installRC, Stderr: "boom"}, nil
		}
		return host.Result{ExitCode: 98}, nil
	}
	return e
}

func (r *codexRig) ran(prefix string) int {
	n := 0
	for _, c := range r.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func codexProject(t *testing.T) (dir, home string) {
	t.Helper()
	dir = newProject(t, `{"work":{"dispatch":"herdr"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	return dir, filepath.Join(dir, ".git", "rota", "codex", "w1")
}

func TestCodexPreflightHappyPathSeedsAndInstalls(t *testing.T) {
	dir, home := codexProject(t)
	rig := &codexRig{loggedIn: true}
	set, err := rig.env(tmuxFake()).Preflight(bg, dir, harness.Codex, "w1", "")
	if err != nil || set.Home != home || set.Version != "0.159.2" || len(set.Warnings) != 0 {
		t.Fatalf("%+v %v", set, err)
	}
	if fi, err := os.Stat(home); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("home = %v %v", fi, err)
	}
	b, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	wt := filepath.Join(dir, ".worktrees", "w1")
	want := "check_for_update_on_startup = false\n\n[projects.\"" + wt + "\"]\ntrust_level = \"trusted\"\n"
	if string(b) != want {
		t.Errorf("config.toml =\n%s\nwant\n%s", b, want)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); err == nil {
		t.Error("rota must never write auth.json")
	}
	for _, c := range []string{"herdr integration status | CODEX_HOME=" + home, "herdr integration install codex | CODEX_HOME=" + home, "codex login status | CODEX_HOME=" + home} {
		if rig.ran(c) != 1 {
			t.Errorf("want one %q in %v", c, rig.calls)
		}
	}
	// Even the version call runs under the slot home, never ~/.codex.
	if rig.calls[0] != "codex --version | CODEX_HOME="+home {
		t.Errorf("version call = %q", rig.calls[0])
	}
}

func TestCodexPreflightKeepsAnExistingConfigAndInstalledIntegration(t *testing.T) {
	dir, home := codexProject(t)
	os.MkdirAll(home, 0o700)
	os.WriteFile(filepath.Join(home, "config.toml"), []byte("# mine\n"), 0o600)
	rig := &codexRig{loggedIn: true, installed: true}
	if _, err := rig.env(tmuxFake()).Preflight(bg, dir, harness.Codex, "w1", ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "config.toml")); string(b) != "# mine\n" {
		t.Errorf("an existing config.toml must stay untouched: %q", b)
	}
	if rig.ran("herdr integration install") != 0 {
		t.Errorf("a current integration is not reinstalled: %v", rig.calls)
	}
	// With the home present, the version runs under it.
	if !strings.Contains(rig.calls[0], "CODEX_HOME="+home) {
		t.Errorf("version call = %q", rig.calls[0])
	}
}

func TestCodexPreflightRefusals(t *testing.T) {
	type rc struct {
		rig  codexRig
		exit int
		by   string
		hint string
		msg  string
	}
	cases := map[string]rc{
		"no codex":       {rig: codexRig{missing: map[string]bool{"codex": true}}, exit: exitcode.ExitUnavailable},
		"no herdr":       {rig: codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}, exit: exitcode.ExitUnavailable},
		"new codex":      {rig: codexRig{version: "codex-cli 0.200.0\n", loggedIn: true}},
		"warnings first": {rig: codexRig{version: "WARNING: x\ncodex-cli 0.160.2\n", loggedIn: true}},
		"unparsable":     {rig: codexRig{version: "hello\n", loggedIn: true}},
		"prerelease":     {rig: codexRig{version: "codex-cli 0.159.2-alpha.1\n", loggedIn: true}},
		"flag missing":   {rig: codexRig{noFlag: "--no-daemon", loggedIn: true}, exit: exitcode.ExitRefused, by: "codex flags", msg: "--no-daemon"},
		"last flag":      {rig: codexRig{noFlag: "--no-alt-screen", loggedIn: true}, exit: exitcode.ExitRefused, by: "codex flags", msg: "--no-alt-screen"},
		"not logged in":  {rig: codexRig{}, exit: exitcode.ExitUnavailable, hint: "codex login"},
		"install fails":  {rig: codexRig{loggedIn: true, installRC: 1}, exit: exitcode.ExitUnavailable, hint: "herdr integration install codex"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir, home := codexProject(t)
			set, err := c.rig.env(tmuxFake()).Preflight(bg, dir, harness.Codex, "w1", "m")
			if c.exit == 0 {
				if err != nil || len(set.Warnings) != 0 {
					t.Fatalf("%+v %v", set, err)
				}
				return
			}
			we, _ := err.(*exitcode.Error)
			if we == nil || we.Exit != c.exit {
				t.Fatalf("want exit %d, got %v", c.exit, err)
			}
			if bd, _ := we.Data.(BlockData); bd.BlockedBy != c.by {
				t.Errorf("blockedBy = %q, want %q", bd.BlockedBy, c.by)
			}
			if !strings.Contains(we.Message, c.msg) {
				t.Errorf("message = %q, want %q", we.Message, c.msg)
			}
			if c.hint != "" && !strings.Contains(we.Hint, c.hint) {
				t.Errorf("hint = %q", we.Hint)
			}
			if strings.Contains(we.Hint, "codex login") && !strings.Contains(we.Hint, "CODEX_HOME="+home) {
				t.Errorf("the login hint names the slot home: %q", we.Hint)
			}
		})
	}
}

func TestCodexPreflightNeedsHerdr(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	rig := &codexRig{loggedIn: true}
	_, err := rig.env(tmuxFake()).Preflight(bg, dir, harness.Codex, "w1", "")
	if exitOf(err) != exitcode.ExitUnavailable || !strings.Contains(err.Error(), "codex workers need work.dispatch=herdr") {
		t.Fatalf("%v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, ".git", "rota", "codex")); serr == nil {
		t.Error("no home is created for a host that cannot run codex workers")
	}
}

// ── dispatch ────────────────────────────────────────────────────────────────

func herdrFake() *fakeHost {
	return &fakeHost{name: "herdr", inSession: true, where: "herdr workspace w9"}
}

func TestDispatchCodexSpawnsWithItsHome(t *testing.T) {
	dir, home := codexProject(t)
	// An account on the slot must not leak into a codex pane.
	Update(dir, func(d *Doc) { d.Slot("w1").Raw().Set("configDir", "/acct") })
	rig := &codexRig{loggedIn: true}
	f := herdrFake()
	res, err := rig.env(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1", Kind: "codex", Model: "gpt-x"})
	if err != nil || res.Kind != "codex" {
		t.Fatalf("%+v %v", res, err)
	}
	keyPath := filepath.Join(home, harness.PromptKeyFile)
	_, _, largs, lerr := host.LaunchArgs(f.spawnOpts.Launch)
	want := append(harness.CodexHookArgs("/opt/rota", keyPath), "--model", "gpt-x", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust", "--no-daemon", "--no-alt-screen")
	if f.spawnOpts.CodexHome != home || f.spawnOpts.ConfigDir != "" || lerr != nil || strings.Join(largs, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("spawn opts = %+v (args %q, want %q)", f.spawnOpts, largs, want)
	}
	if !strings.Contains(largs[3], "/opt/rota worker prompt-check --key "+keyPath) {
		t.Errorf("hook = %s", largs[3])
	}
}

func TestDispatchKindDefaultsToTheSlotsRecordedKind(t *testing.T) {
	dir, home := codexProject(t)
	Update(dir, func(d *Doc) { d.Slot("w1").Raw().Set("kind", "codex") })
	rig := &codexRig{loggedIn: true}
	f := herdrFake()
	res, err := rig.env(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1"})
	if err != nil || res.Kind != "codex" || f.spawnOpts.CodexHome != home || strings.Contains(f.spawnOpts.Launch, "--model") {
		t.Fatalf("%+v %v %+v", res, err, f.spawnOpts)
	}
	// An explicit kind beats the recorded one.
	f = herdrFake()
	res, err = rig.env(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T2", Kind: "claude"})
	if err != nil || res.Kind != "claude" || f.spawnOpts.CodexHome != "" || !strings.HasPrefix(f.spawnOpts.Launch, "claude ") {
		t.Fatalf("%+v %v %+v", res, err, f.spawnOpts)
	}
}

func TestDispatchCodexRefusalsTouchNothing(t *testing.T) {
	cases := map[string]struct {
		cfg     string
		rig     codexRig
		kind    string
		model   string
		exit    int
		by, msg string
	}{
		"resume":                    {cfg: `{"work":{"dispatch":"herdr","codexCommand":"codex resume --last"}}`, rig: codexRig{loggedIn: true}, exit: exitcode.ExitRefused, by: "resume flag", msg: "subcommand 'resume'"},
		"fork":                      {cfg: `{"work":{"dispatch":"herdr","codexCommand":"codex --model x fork"}}`, rig: codexRig{loggedIn: true}, exit: exitcode.ExitRefused, by: "resume flag", msg: "'fork'"},
		"unparseable":               {cfg: `{"work":{"dispatch":"herdr","codexCommand":"codex \"oops"}}`, rig: codexRig{loggedIn: true}, exit: exitcode.ExitUsage, msg: "work.codexCommand cannot be parsed"},
		"model needed":              {cfg: `{"work":{"dispatch":"herdr","codexCommand":"codex -m {model}"}}`, rig: codexRig{loggedIn: true}, exit: exitcode.ExitUsage, msg: "{model}"},
		"wrong binary":              {cfg: `{"work":{"dispatch":"herdr","codexCommand":"claude --x"}}`, rig: codexRig{loggedIn: true}, exit: exitcode.ExitUnavailable, msg: "does not run codex"},
		"claude kind, codex binary": {cfg: `{"work":{"dispatch":"herdr","workerCommand":"codex --x"}}`, rig: codexRig{loggedIn: true}, kind: "claude", exit: exitcode.ExitUnavailable, msg: "does not run claude"},
		"flag":                      {cfg: `{"work":{"dispatch":"herdr"}}`, rig: codexRig{noFlag: "--no-daemon", loggedIn: true}, exit: exitcode.ExitRefused, by: "codex flags", msg: "--no-daemon"},
		"login":                     {cfg: `{"work":{"dispatch":"herdr"}}`, rig: codexRig{}, exit: exitcode.ExitUnavailable, msg: "not logged in"},
		"bad kind":                  {cfg: `{"work":{"dispatch":"herdr"}}`, rig: codexRig{loggedIn: true}, kind: "gemini", exit: exitcode.ExitUsage, msg: "claude or codex"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := newProject(t, c.cfg)
			goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
			kind := c.kind
			if kind == "" {
				kind = "codex"
			}
			f := herdrFake()
			_, err := c.rig.env(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1", Kind: kind, Model: c.model})
			we, _ := err.(*exitcode.Error)
			if we == nil || we.Exit != c.exit || !strings.Contains(err.Error(), c.msg) {
				t.Fatalf("err = %v, want exit %d with %q", err, c.exit, c.msg)
			}
			if bd, _ := we.Data.(BlockData); bd.BlockedBy != c.by {
				t.Errorf("blockedBy = %q, want %q", bd.BlockedBy, c.by)
			}
			if len(f.calls) != 0 {
				t.Errorf("the old session was touched: %v", f.calls)
			}
			if slotField(t, dir, "w1", "task") != "<null>" {
				t.Errorf("the slot was marked")
			}
		})
	}
}

// Codex's version is never gated on: a release past the one rota was tested on
// dispatches without a flag and without a warning.
func TestDispatchCodexNewVersionPasses(t *testing.T) {
	dir, _ := codexProject(t)
	rig := &codexRig{version: "codex-cli 0.200.0\n", loggedIn: true}
	res, err := rig.env(herdrFake()).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1", Kind: "codex"})
	if err != nil || len(res.Warnings) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

// A custom launch line is probed for its own flags only.
func TestDispatchCodexCustomCommandProbesItsFlags(t *testing.T) {
	for name, c := range map[string]struct {
		cmd, by string
	}{
		"listed flags only": {cmd: "codex --dangerously-bypass-hook-trust --model x"},
		"unlisted flag":     {cmd: "codex --dangerously-bypass-hook-trust --made-up=1", by: "codex flags"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := newProject(t, `{"work":{"dispatch":"herdr","codexCommand":"`+c.cmd+`"}}`)
			goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
			_, err := (&codexRig{noFlag: "--no-daemon", loggedIn: true}).env(tmuxFake()).Preflight(bg, dir, harness.Codex, "w1", "")
			if c.by == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			we, _ := err.(*exitcode.Error)
			if bd, _ := we.Data.(BlockData); we == nil || bd.BlockedBy != c.by || !strings.Contains(we.Message, "--made-up") {
				t.Fatalf("%v", err)
			}
		})
	}
}

// A relay goes into the running session: kind, launch command and codex are
// not consulted.
func TestDispatchRelayIgnoresKind(t *testing.T) {
	dir, _ := codexProject(t)
	rig := &codexRig{missing: map[string]bool{"codex": true}}
	f := herdrFake()
	e := rig.env(f)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1", Kind: "claude"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "q"), Relay: true, Kind: "codex"})
	if err != nil || res.Kind != "" || len(rig.calls) != 0 {
		t.Fatalf("%+v %v %v", res, err, rig.calls)
	}
}

// ── prompt signing (#3) ─────────────────────────────────────────────────────

func keyOf(t *testing.T, home string) []byte {
	t.Helper()
	k, err := harness.LoadPromptKey(filepath.Join(home, harness.PromptKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestDispatchCodexSignsItsBriefAndRotatesTheKey(t *testing.T) {
	dir, home := codexProject(t)
	rig := &codexRig{loggedIn: true}
	f := herdrFake()
	e := rig.env(f)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "do it\n"), Task: "T1", Kind: "codex"}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(home, harness.PromptKeyFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", fi, err)
	}
	k1 := keyOf(t, home)
	if ok, why := harness.CheckPrompt(k1, f.sent); !ok {
		t.Fatalf("sent payload does not verify: %s\n%s", why, f.sent)
	}
	if !strings.HasPrefix(f.sent, "--- ORCHESTRATOR (round 1) ---\n") {
		t.Errorf("header lost: %q", f.sent)
	}
	if slotField(t, dir, "w1", "kind") != "codex" {
		t.Errorf("kind not recorded")
	}
	// A second task rotates the key.
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "again\n"), Task: "T2", Kind: "codex"}); err != nil {
		t.Fatal(err)
	}
	k2 := keyOf(t, home)
	if string(k1) == string(k2) {
		t.Error("the key did not rotate")
	}
	if ok, _ := harness.CheckPrompt(k1, f.sent); ok {
		t.Error("the old key still verifies the new brief")
	}
	// A relay is signed with the current key.
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "answer\n"), Relay: true}); err != nil {
		t.Fatal(err)
	}
	if ok, why := harness.CheckPrompt(k2, f.sent); !ok || !strings.Contains(f.sent, "ORCHESTRATOR RELAY") {
		t.Fatalf("relay: %v %s\n%s", ok, why, f.sent)
	}
}

func TestDispatchCodexRelayWithoutKeyFailsBeforeSending(t *testing.T) {
	dir, home := codexProject(t)
	rig := &codexRig{loggedIn: true}
	f := herdrFake()
	e := rig.env(f)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1", Kind: "codex"}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(home, harness.PromptKeyFile))
	f.calls, f.sent = nil, ""
	_, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "q\n"), Relay: true})
	if exitOf(err) != exitcode.ExitUnavailable || !strings.Contains(err.(*exitcode.Error).Hint, "re-dispatch") {
		t.Fatalf("%v", err)
	}
	if len(f.calls) != 0 || f.sent != "" {
		t.Errorf("something was sent: %v %q", f.calls, f.sent)
	}
	if v := slotField(t, dir, "w1", "relays"); v != "[]" && v != "<null>" {
		t.Errorf("relay was logged: %s", v)
	}
}

func TestDispatchCodexNeedsTheHookTrustFlag(t *testing.T) {
	dir := newProject(t, `{"work":{"dispatch":"herdr","codexCommand":"codex --yolo"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	rig := &codexRig{loggedIn: true}
	f := herdrFake()
	_, err := rig.env(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1", Kind: "codex"})
	if exitOf(err) != exitcode.ExitUnavailable || !strings.Contains(err.Error(), "--dangerously-bypass-hook-trust") {
		t.Fatalf("%v", err)
	}
	if len(f.calls) != 0 || len(rig.calls) != 0 || slotField(t, dir, "w1", "task") != "<null>" {
		t.Errorf("state touched: %v %v", f.calls, rig.calls)
	}
}

func TestDispatchClaudePayloadIsNotSigned(t *testing.T) {
	dir, home := codexProject(t)
	rig := &codexRig{loggedIn: true}
	f := herdrFake()
	if _, err := rig.env(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1", Kind: "claude"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.sent, "ROTA-SIG") || strings.Contains(f.spawnOpts.Launch, "prompt-check") {
		t.Errorf("claude path changed: %q / %q", f.sent, f.spawnOpts.Launch)
	}
	if _, err := os.Stat(filepath.Join(home, harness.PromptKeyFile)); err == nil {
		t.Error("a claude dispatch wrote a key")
	}
}
