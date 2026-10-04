package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

func TestCodexCommand(t *testing.T) {
	cases := []struct {
		cfg, model, want string
		exit             int
	}{
		{``, "gpt-x", "codex --model gpt-x --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen", 0},
		{``, "", "codex --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen", 0},
		{`{"work":{"codexCommand":"wrap codex -m {model}"}}`, "gpt-x", "wrap codex -m gpt-x", 0},
		{`{"work":{"codexCommand":"wrap codex -m {model}"}}`, "", "", ExitUsage},
		{`{"work":{"codexCommand":"codex --yolo"}}`, "gpt-x", "codex --yolo", 0},
		{`{"work":{"codexCommand":"codex --yolo"}}`, "", "codex --yolo", 0},
		{`{"work":{"workerCommand":"claude -x"}}`, "gpt-x", "codex --model gpt-x --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen", 0},
	}
	for _, c := range cases {
		got, err := codexCommand(launchRoot(t, c.cfg), c.model)
		if c.exit != 0 {
			if exitOf(err) != c.exit {
				t.Errorf("%s + %q: err = %v, want exit %d", c.cfg, c.model, err, c.exit)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s + %q: %q, %v; want %q", c.cfg, c.model, got, err, c.want)
		}
	}
	if !strings.Contains(DefaultCodexCommand, "--model {model} ") {
		t.Errorf("the default must carry the droppable --model {model} pair: %s", DefaultCodexCommand)
	}
}

func TestModelAppliesToCodex(t *testing.T) {
	for cfg, want := range map[string]bool{``: true, `{"work":{"codexCommand":"codex -x"}}`: false, `{"work":{"codexCommand":"codex -m {model}"}}`: true, `{"work":{"workerCommand":"claude -x"}}`: true} {
		if got := ModelAppliesTo(launchRoot(t, cfg), KindCodex); got != want {
			t.Errorf("%s: %v want %v", cfg, got, want)
		}
	}
}

func TestCodexResume(t *testing.T) {
	for cmd, want := range map[string]string{
		"codex --model x --dangerously-bypass-approvals-and-sandbox": "",
		"codex resume --last":                   "resume",
		"codex fork abc":                        "fork",
		"codex --model x resume":                "resume",
		"A=1 /opt/bin/codex -c key=v resume":    "resume",
		`sh -c "codex resume"`:                  "resume",
		"codex -c model=x -r":                   "", // claude's resume flags mean nothing to codex
		"resume codex --yolo":                   "", // before the binary is not ours to judge
		"codex --model resumed --no-daemon":     "",
		"wrap --kind fork -- codex --no-daemon": "",
	} {
		got, err := CodexResume(cmd)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", cmd, got, err, want)
		}
	}
	if _, err := CodexResume(`codex "oops`); err == nil {
		t.Error("an unbalanced quote must be an error")
	}
}

func TestTomlString(t *testing.T) {
	for in, want := range map[string]string{
		`/a/b`:        `"/a/b"`,
		`/a "q" \ b`:  `"/a \"q\" \\ b"`,
		"/a\nb\tc":    `"/a\nb\tc"`,
		"/a\x01b\x7f": `"/a\u0001b\u007F"`,
		"/ünï/日本":     `"/ünï/日本"`,
	} {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}

// ── preflight ───────────────────────────────────────────────────────────────

// codexRig scripts codex and herdr: every call is logged with its env, and no
// real binary is reachable (Env.Run and Env.LookPath replace exec).
type codexRig struct {
	calls     []string
	version   string // `codex --version` stdout; "" means codex-cli 0.159.2
	loggedIn  bool
	installed bool // herdr reports codex current
	installRC int
	missing   map[string]bool
}

func (r *codexRig) env(h host.Host) Env {
	e := envWith(h)
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
	set, err := rig.env(tmuxFake()).CodexPreflight(bg, dir, "w1", false)
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
	if _, err := rig.env(tmuxFake()).CodexPreflight(bg, dir, "w1", false); err != nil {
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
		rig    codexRig
		accept bool
		exit   int
		by     string
		hint   string
		warn   string
	}
	cases := map[string]rc{
		"no codex":       {rig: codexRig{missing: map[string]bool{"codex": true}}, exit: ExitUnavailable},
		"no herdr":       {rig: codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}, exit: ExitUnavailable},
		"too old":        {rig: codexRig{version: "codex-cli 0.158.9\n", loggedIn: true}, exit: ExitRefused, by: "codex version"},
		"too new":        {rig: codexRig{version: "codex-cli 0.160.0\n", loggedIn: true}, exit: ExitRefused, by: "codex version"},
		"warnings first": {rig: codexRig{version: "WARNING: x\ncodex-cli 0.160.2\n", loggedIn: true}, accept: true, warn: "codex 0.160.2 is outside the supported range >=0.159.0 <0.160.0"},
		"in range":       {rig: codexRig{version: "WARNING: update available\ncodex-cli 0.159.0\n", loggedIn: true}},
		"unreadable":     {rig: codexRig{version: "hello\n", loggedIn: true}, exit: ExitRefused, by: "codex version"},
		"unreadable ok":  {rig: codexRig{version: "hello\n", loggedIn: true}, accept: true, warn: "codex (version unreadable) is outside the supported range >=0.159.0 <0.160.0"},
		"prerelease":     {rig: codexRig{version: "codex-cli 0.159.2-alpha.1\n", loggedIn: true}, exit: ExitRefused, by: "codex version"},
		"not logged in":  {rig: codexRig{}, exit: ExitUnavailable, hint: "codex login"},
		"install fails":  {rig: codexRig{loggedIn: true, installRC: 1}, exit: ExitUnavailable, hint: "herdr integration install codex"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir, home := codexProject(t)
			set, err := c.rig.env(tmuxFake()).CodexPreflight(bg, dir, "w1", c.accept)
			if c.exit == 0 {
				if err != nil || (c.warn != "" && (len(set.Warnings) != 1 || set.Warnings[0] != c.warn)) || (c.warn == "" && len(set.Warnings) != 0) {
					t.Fatalf("%+v %v", set, err)
				}
				return
			}
			we, _ := err.(*Error)
			if we == nil || we.Exit != c.exit {
				t.Fatalf("want exit %d, got %v", c.exit, err)
			}
			if bd, _ := we.Data.(BlockData); bd.BlockedBy != c.by {
				t.Errorf("blockedBy = %q, want %q", bd.BlockedBy, c.by)
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
	_, err := rig.env(tmuxFake()).CodexPreflight(bg, dir, "w1", false)
	if exitOf(err) != ExitUnavailable || !strings.Contains(err.Error(), "codex workers need work.dispatch=herdr") {
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
	Update(dir, nil, func(doc *jsonx.Object) { (Registry{Doc: doc}).Slot("w1").Set("configDir", "/acct") })
	rig := &codexRig{loggedIn: true}
	f := herdrFake()
	res, err := rig.env(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1", Kind: "codex", Model: "gpt-x"})
	if err != nil || res.Kind != "codex" {
		t.Fatalf("%+v %v", res, err)
	}
	if f.spawnOpts.CodexHome != home || f.spawnOpts.ConfigDir != "" ||
		f.spawnOpts.Launch != "codex --model gpt-x --dangerously-bypass-approvals-and-sandbox --dangerously-bypass-hook-trust --no-daemon --no-alt-screen" {
		t.Errorf("spawn opts = %+v", f.spawnOpts)
	}
}

func TestDispatchKindDefaultsToTheSlotsRecordedKind(t *testing.T) {
	dir, home := codexProject(t)
	Update(dir, nil, func(doc *jsonx.Object) { (Registry{Doc: doc}).Slot("w1").Set("kind", "codex") })
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
		"resume":                    {cfg: `{"work":{"dispatch":"herdr","codexCommand":"codex resume --last"}}`, rig: codexRig{loggedIn: true}, exit: ExitRefused, by: "resume flag", msg: "subcommand 'resume'"},
		"fork":                      {cfg: `{"work":{"dispatch":"herdr","codexCommand":"codex --model x fork"}}`, rig: codexRig{loggedIn: true}, exit: ExitRefused, by: "resume flag", msg: "'fork'"},
		"unparseable":               {cfg: `{"work":{"dispatch":"herdr","codexCommand":"codex \"oops"}}`, rig: codexRig{loggedIn: true}, exit: ExitUsage, msg: "work.codexCommand cannot be parsed"},
		"model needed":              {cfg: `{"work":{"dispatch":"herdr","codexCommand":"codex -m {model}"}}`, rig: codexRig{loggedIn: true}, exit: ExitUsage, msg: "{model}"},
		"wrong binary":              {cfg: `{"work":{"dispatch":"herdr","codexCommand":"claude --x"}}`, rig: codexRig{loggedIn: true}, exit: ExitUnavailable, msg: "does not run codex"},
		"claude kind, codex binary": {cfg: `{"work":{"dispatch":"herdr","workerCommand":"codex --x"}}`, rig: codexRig{loggedIn: true}, kind: "claude", exit: ExitUnavailable, msg: "does not run claude"},
		"version":                   {cfg: `{"work":{"dispatch":"herdr"}}`, rig: codexRig{version: "codex-cli 0.200.0\n", loggedIn: true}, exit: ExitRefused, by: "codex version", msg: "outside the supported range"},
		"login":                     {cfg: `{"work":{"dispatch":"herdr"}}`, rig: codexRig{}, exit: ExitUnavailable, msg: "not logged in"},
		"bad kind":                  {cfg: `{"work":{"dispatch":"herdr"}}`, rig: codexRig{loggedIn: true}, kind: "gemini", exit: ExitUsage, msg: "claude or codex"},
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
			we, _ := err.(*Error)
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

func TestDispatchCodexAcceptVersionPasses(t *testing.T) {
	dir, _ := codexProject(t)
	rig := &codexRig{version: "codex-cli 0.200.0\n", loggedIn: true}
	res, err := rig.env(herdrFake()).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1", Kind: "codex", AcceptCodexVersion: true})
	if err != nil || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "codex 0.200.0 is outside the supported range") {
		t.Fatalf("%+v %v", res, err)
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
