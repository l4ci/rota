package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/skills"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fake answers by "<tool> <args>" prefix; a tool missing from have is not on PATH.
type fake struct {
	have  map[string]bool
	reply map[string]Result // key: tool + " " + joined args
	envs  []string          // CLAUDE_CONFIG_DIR values seen by herdr integration status
}

func (f *fake) look(name string) (string, bool) { return "/fake/" + name, f.have[name] }

func (f *fake) exec(_ context.Context, bin string, args []string, env []string, _ string) (Result, error) {
	tool := filepath.Base(bin)
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "CLAUDE_CONFIG_DIR="); ok {
			f.envs = append(f.envs, v)
		}
	}
	if r, ok := f.reply[tool+" "+strings.Join(args, " ")]; ok {
		return r, nil
	}
	return Result{ExitCode: 1}, nil
}

func statusOf(r Report, name string) Check {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return Check{}
}

func TestParseIntegration(t *testing.T) {
	cur, miss := fixture(t, "integration_status_current.txt"), fixture(t, "integration_status_missing.txt")
	for _, tc := range []struct{ name, out, agent, want string }{
		{"current", cur, "claude", "current"},
		{"not installed", miss, "claude", "not installed"},
		{"other agent untouched", miss, "codex", "current"},
		{"absent agent", cur, "nope", "no status"},
		{"empty output", "", "claude", "no status"},
		{"case and spacing", "  Claude :  Current (v11) (/x)\n", "claude", "current"},
		{"outdated", "claude: outdated (v9, latest v10) (/x)\n", "claude", "outdated"},
		{"no version suffix", "claude: not installed\n", "claude", "not installed"},
	} {
		if got := ParseIntegration(tc.out, tc.agent); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRunTable(t *testing.T) {
	home := t.TempDir()
	good := filepath.Join(home, "good")
	os.MkdirAll(good, 0o755)
	os.WriteFile(filepath.Join(good, ".credentials.json"), []byte("{}"), 0o600)
	nocred := filepath.Join(home, "nocred")
	os.MkdirAll(nocred, 0o755)
	cur, miss := fixture(t, "integration_status_current.txt"), fixture(t, "integration_status_missing.txt")

	all := map[string]bool{"git": true, "herdr": true, "tmux": true, "gh": true, "glab": true}
	base := map[string]Result{
		"git check-ignore -q .worktrees/x": {},
		"git remote get-url origin":        {Stdout: "git@github.com:a/b.git\n"},
		"gh auth status":                   {},
		"glab auth status":                 {},
		"herdr --version":                  {Stdout: "herdr 0.9.3\n"},
		"herdr integration status":         {Stdout: cur},
	}
	with := func(over map[string]Result) map[string]Result {
		m := map[string]Result{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	without := func(names ...string) map[string]bool {
		m := map[string]bool{}
		for k := range all {
			m[k] = true
		}
		for _, n := range names {
			delete(m, n)
		}
		return m
	}
	herdrIn := Input{Dispatch: "herdr", Accounts: []Account{{"a", good}}}
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i+1 < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	inHerdr := Input{Dispatch: "subagent", Getenv: env("HERDR_ENV", "1"), Accounts: []Account{{"a", good}}}

	for _, tc := range []struct {
		name   string
		in     Input
		have   map[string]bool
		reply  map[string]Result
		check  string
		status string
		detail string // substring
		hint   string // substring
	}{
		{"git ok", Input{}, all, with(nil), "git", Pass, "gitignored", ""},
		{"git missing", Input{}, without("git"), with(nil), "git", Fail, "not found", "install git"},
		{"worktrees not ignored", Input{}, all, with(map[string]Result{"git check-ignore -q .worktrees/x": {ExitCode: 1}}), "git", Fail, "not gitignored", "rota init"},
		{"not a repo", Input{}, all, with(map[string]Result{"git check-ignore -q .worktrees/x": {ExitCode: 128}}), "git", Fail, "not inside", "git init"},

		{"host subagent skips", Input{}, all, with(nil), "host", Skip, "subagent", ""},
		{"host herdr ok", herdrIn, all, with(nil), "host", Pass, "herdr 0.9.3", ""},
		{"host herdr 0.8", herdrIn, all, with(map[string]Result{"herdr --version": {Stdout: "herdr 0.8.2"}}), "host", Fail, "herdr 0.8.2, need 0.9.x", "0.9.x"},
		{"host herdr 0.10", herdrIn, all, with(map[string]Result{"herdr --version": {Stdout: "herdr 0.10.0"}}), "host", Fail, "need 0.9.x", ""},
		{"host herdr garbage", herdrIn, all, with(map[string]Result{"herdr --version": {Stdout: "??"}}), "host", Fail, "unreadable", ""},
		{"host herdr missing", herdrIn, without("herdr"), with(nil), "host", Fail, "not found", ""},
		{"host tmux ok", Input{Dispatch: "tmux"}, all, with(nil), "host", Pass, "tmux", ""},
		{"host tmux missing", Input{Dispatch: "tmux"}, without("tmux"), with(nil), "host", Fail, "tmux not found", "install tmux"},

		{"host detects herdr in a pane", inHerdr, all, with(nil), "host", Pass, "herdr 0.9.3", ""},
		{"host detects herdr when dispatch is unset", Input{Getenv: env("HERDR_ENV", "1")}, all, with(nil), "host", Pass, "herdr 0.9.3", ""},
		{"host detects tmux inside tmux", Input{Dispatch: "subagent", Getenv: env("TMUX", "/tmp/tmux-1/default,1,0")}, all, with(nil), "host", Pass, "tmux on PATH", ""},
		{"host herdr pane without herdr falls to solo", Input{Dispatch: "subagent", Getenv: env("HERDR_ENV", "1")}, without("herdr"), with(nil), "host", Skip, "solo", ""},
		{"host herdr pane prefers herdr over tmux", Input{Getenv: env("HERDR_ENV", "1", "TMUX", "x")}, all, with(nil), "host", Pass, "herdr", ""},
		{"host explicit tmux ignores a herdr pane", Input{Dispatch: "tmux", Getenv: env("HERDR_ENV", "1")}, all, with(nil), "host", Pass, "tmux", ""},
		{"host explicit herdr missing fails in tmux", Input{Dispatch: "herdr", Getenv: env("TMUX", "x")}, without("herdr"), with(nil), "host", Fail, "herdr not found", ""},

		{"tracker github ok", Input{}, all, with(nil), "tracker", Pass, "gh authenticated", ""},
		{"tracker gh unauthenticated", Input{}, all, with(map[string]Result{"gh auth status": {ExitCode: 1}}), "tracker", Fail, "not authenticated", "gh auth login"},
		{"tracker gh missing", Input{}, without("gh"), with(nil), "tracker", Fail, "gh not found", ""},
		{"tracker gitlab", Input{}, all, with(map[string]Result{"git remote get-url origin": {Stdout: "https://gitlab.com/a/b"}}), "tracker", Pass, "glab", ""},
		{"tracker no remote", Input{}, all, with(map[string]Result{"git remote get-url origin": {ExitCode: 2}}), "tracker", Skip, "no origin", ""},
		{"tracker config fallback", Input{IssuesProvider: "gitlab"}, all, with(map[string]Result{"git remote get-url origin": {ExitCode: 2}}), "tracker", Pass, "glab", ""},

		{"accounts none", Input{}, all, with(nil), "accounts", Skip, "no accounts", ""},
		{"accounts ok", Input{Accounts: []Account{{"a", good}}}, all, with(nil), "accounts", Pass, "1 accounts", ""},
		{"accounts tilde", Input{Home: home, Accounts: []Account{{"a", "~/good"}}}, all, with(nil), "accounts", Pass, "", ""},
		{"accounts no dir", Input{Accounts: []Account{{"a", filepath.Join(home, "gone")}}}, all, with(nil), "accounts", Fail, "does not exist", "claude /login"},
		{"accounts no creds", Input{Accounts: []Account{{"a", nocred}}}, all, with(nil), "accounts", Fail, "no credentials file", "CLAUDE_CONFIG_DIR="},
		{"accounts no configDir", Input{Accounts: []Account{{"a", ""}}}, all, with(nil), "accounts", Fail, "no configDir", "work.accounts"},

		{"hook ok", herdrIn, all, with(nil), "hook", Pass, "a: current", ""},
		{"hook not installed", herdrIn, all, with(map[string]Result{"herdr integration status": {Stdout: miss}}), "hook", Fail, "a: not installed", "herdr integration install claude"},
		{"hook status errors", herdrIn, all, with(map[string]Result{"herdr integration status": {ExitCode: 1}}), "hook", Fail, "status failed", "herdr integration install claude"},
		{"hook runs for a detected herdr pane", inHerdr, all, with(nil), "hook", Pass, "a: current", ""},
		{"hook skips for detected tmux", Input{Dispatch: "subagent", Getenv: env("TMUX", "x"), Accounts: []Account{{"a", good}}}, all, with(nil), "hook", Skip, "not herdr", ""},
		{"hook skips off herdr", Input{Dispatch: "tmux", Accounts: []Account{{"a", good}}}, all, with(nil), "hook", Skip, "not herdr", ""},
		{"hook skips without accounts", Input{Dispatch: "herdr"}, all, with(nil), "hook", Skip, "no accounts", ""},
		{"hook skips without herdr", herdrIn, without("herdr"), with(nil), "hook", Skip, "see host", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{have: tc.have, reply: tc.reply}
			in := tc.in
			in.Exec, in.Look = f.exec, f.look
			c := statusOf(Run(context.Background(), in), tc.check)
			if c.Status != tc.status {
				t.Fatalf("status %q, want %q (%+v)", c.Status, tc.status, c)
			}
			if !strings.Contains(c.Detail, tc.detail) {
				t.Errorf("detail %q lacks %q", c.Detail, tc.detail)
			}
			if tc.status == Fail && c.Hint == "" {
				t.Error("a fail carries a hint")
			}
			if !strings.Contains(c.Hint, tc.hint) {
				t.Errorf("hint %q lacks %q", c.Hint, tc.hint)
			}
		})
	}
}

func TestHookRunsPerAccountWithItsConfigDir(t *testing.T) {
	d := t.TempDir()
	f := &fake{have: map[string]bool{"herdr": true}, reply: map[string]Result{
		"herdr integration status": {Stdout: fixture(t, "integration_status_current.txt")},
	}}
	in := Input{Dispatch: "herdr", Home: d, Accounts: []Account{{"a", "~/one"}, {"b", "/two"}}, Exec: f.exec, Look: f.look}
	c := statusOf(Run(context.Background(), in), "hook")
	if c.Status != Pass {
		t.Fatalf("%+v", c)
	}
	want := []string{filepath.Join(d, "one"), "/two"}
	if strings.Join(f.envs, "|") != strings.Join(want, "|") {
		t.Errorf("config dirs %v, want %v", f.envs, want)
	}
}

func TestOrderAndOK(t *testing.T) {
	f := &fake{have: map[string]bool{}, reply: map[string]Result{}}
	r := Run(context.Background(), Input{Exec: f.exec, Look: f.look})
	var names []string
	for _, c := range r.Checks {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "git,host,tracker,accounts,hook,statusline,stop-hook,switch,skills,codex" {
		t.Errorf("order %s", got)
	}
	if r.OK() {
		t.Error("git is missing, so the report is not OK")
	}
	if !(Report{Checks: []Check{{Status: Pass}, {Status: Skip}}}).OK() {
		t.Error("pass and skip are OK")
	}
}

func TestSkillsCheck(t *testing.T) {
	const bin = "aaaaaaaaaaaaaaaa"
	root := func(mod func(*skills.RootStatus)) skills.RootStatus {
		r := skills.RootStatus{Root: skills.Root{Path: "/h/.claude/skills", Agent: "claude", Scope: "user"},
			Installed: true, Version: "5.0.0", Digest: bin, Current: true}
		if mod != nil {
			mod(&r)
		}
		return r
	}
	rep := func(roots ...skills.RootStatus) *skills.Report {
		return &skills.Report{Version: "5.0.0", Digest: bin, Roots: roots}
	}
	f := &fake{have: map[string]bool{}}
	for _, tc := range []struct {
		name   string
		in     *skills.Report
		status string
		detail string // substring
		hint   string
	}{
		{"nothing read", nil, Skip, "rota skills install", ""},
		{"not installed", rep(root(func(r *skills.RootStatus) { r.Installed = false })), Skip, "rota skills install", ""},
		{"current", rep(root(nil)), Pass, "match rota 5.0.0", ""},
		{"mismatch", rep(root(func(r *skills.RootStatus) { r.Version, r.Digest, r.Current = "4.5.0", "bbbb", false })), Fail, "skills 4.5.0, rota 5.0.0", "run: rota skills update"},
		{"dev build mismatch", func() *skills.Report {
			r := rep(root(func(r *skills.RootStatus) { r.Version, r.Digest, r.Current = "", "bbbbbbbbbbbbbbbbbbbb", false }))
			r.Version = ""
			return r
		}(), Fail, "skills bbbbbbbbbbbb, rota aaaaaaaaaaaa", "run: rota skills update"},
		{"edited", rep(root(func(r *skills.RootStatus) { r.Edited = []string{"rota-work/SKILL.md"} })), Fail, "1 edited (rota-work/SKILL.md)", "run: rota skills update --overwrite"},
		{"missing", rep(root(func(r *skills.RootStatus) { r.Missing = []string{"rota-work/SKILL.md", "rota-ship/SKILL.md"} })), Fail, "2 missing", "run: rota skills update"},
		{"second root only", rep(root(func(r *skills.RootStatus) { r.Installed = false }), root(func(r *skills.RootStatus) { r.Path = "/h/.agents/skills" })), Pass, "1 roots", ""},
	} {
		c := statusOf(Run(context.Background(), Input{Skills: tc.in, Exec: f.exec, Look: f.look}), "skills")
		if c.Status != tc.status || !strings.Contains(c.Detail, tc.detail) || (tc.hint != "" && c.Hint != tc.hint) {
			t.Errorf("%s: %+v, want %s %q hint %q", tc.name, c, tc.status, tc.detail, tc.hint)
		}
		if tc.status == Fail && c.Hint == "" {
			t.Errorf("%s: a fail needs a hint", tc.name)
		}
	}
}

func TestParseCodexVersion(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		want      string // "" means unparseable
		inRange   bool
	}{
		{"plain", "codex-cli 0.159.2\n", "0.159.2", true},
		{"lower bound", "codex-cli 0.159.0", "0.159.0", true},
		{"below", "codex-cli 0.158.99\n", "0.158.99", false},
		{"upper bound is exclusive", "codex-cli 0.160.0\n", "0.160.0", false},
		{"next major", "codex-cli 1.0.0\n", "1.0.0", false},
		{"warning lines around it", "WARNING: proceeding\ncodex-cli 0.159.5\nWARNING: x\n", "0.159.5", true},
		{"stderr joined", "\nWARNING: Failed to load config\ncodex-cli 0.159.1", "0.159.1", true},
		{"prerelease is not a version", "codex-cli 0.159.2-alpha.1\n", "", false},
		{"other tool", "claude 2.1.0\n", "", false},
		{"bare number", "0.159.2\n", "", false},
		{"empty", "", "", false},
	} {
		v, ok := ParseCodexVersion(tc.out)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: parsed %v from %q", tc.name, v, tc.out)
			}
			continue
		}
		if !ok || v.String() != tc.want || v.InRange() != tc.inRange {
			t.Errorf("%s: got %v %v inRange %v, want %s %v", tc.name, v, ok, v.InRange(), tc.want, tc.inRange)
		}
	}
}

func TestCodexCheck(t *testing.T) {
	// the claude fixtures carry a codex line of their own, so build these here
	codexCur := "claude: current (v10)\ncodex: current (v8) (/x)\n"
	codexMiss := "claude: current (v10)\ncodex: not installed (/x)\n"
	homes := []CodexHome{{"ben", "/cd/rota/codex/ben"}, {"dana", "/cd/rota/codex/dana"}}
	ver := Result{Stdout: "codex-cli 0.159.2\n"}
	for _, tc := range []struct {
		name     string
		have     []string
		dispatch string
		homes    []CodexHome
		reply    map[string]Result
		status   string
		detail   string // substring
		hint     string
	}{
		{"skip: no codex, no homes", nil, "herdr", nil, nil, Skip, "no slot has a codex home", ""},
		{"pass: codex alone", []string{"codex"}, "", nil, map[string]Result{"codex --version": ver}, Pass, "codex 0.159.2, no slot homes yet; round.tiers.codex unset (optional)", ""},
		{"fail: homes but no codex", nil, "herdr", homes, nil, Fail, "codex not found on PATH", CodexInstallHint},
		{"fail: version unreadable", []string{"codex"}, "", nil, map[string]Result{"codex --version": {Stdout: "hello"}}, Fail, "unreadable", CodexInstallHint},
		{"fail: version command fails", []string{"codex"}, "", nil, map[string]Result{"codex --version": {ExitCode: 3, Stdout: "codex-cli 0.159.2"}}, Fail, "unreadable", CodexInstallHint},
		{"fail: out of range", []string{"codex"}, "", nil, map[string]Result{"codex --version": {Stdout: "codex-cli 0.160.1"}}, Fail, "codex 0.160.1, need >=0.159.0 <0.160.0", CodexInstallHint},
		{"pass: logged in, herdr integration current", []string{"codex", "herdr"}, "herdr", homes,
			map[string]Result{"codex --version": ver, "codex login status": {}, "herdr integration status": {Stdout: codexCur}},
			Pass, "homes checked: ben, dana; round.tiers.codex unset (optional)", ""},
		{"pass: tmux skips the integration", []string{"codex", "herdr"}, "tmux", homes,
			map[string]Result{"codex --version": ver, "codex login status": {}, "herdr integration status": {Stdout: codexMiss}},
			Pass, "ben, dana", ""},
		{"fail: not logged in", []string{"codex", "herdr"}, "herdr", homes,
			map[string]Result{"codex --version": ver, "codex login status": {ExitCode: 1}, "herdr integration status": {Stdout: codexCur}},
			Fail, "ben: not logged in", "CODEX_HOME=/cd/rota/codex/ben codex login"},
		{"fail: integration missing", []string{"codex", "herdr"}, "herdr", homes[:1],
			map[string]Result{"codex --version": ver, "codex login status": {}, "herdr integration status": {Stdout: codexMiss}},
			Fail, "ben: herdr integration not current", "CODEX_HOME=/cd/rota/codex/ben herdr integration install codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			have := map[string]bool{}
			for _, n := range tc.have {
				have[n] = true
			}
			f := &codexFake{fake: fake{have: have, reply: tc.reply}}
			c := statusOf(Run(context.Background(), Input{Dispatch: tc.dispatch, CodexHomes: tc.homes, Exec: f.exec, Look: f.look}), "codex")
			if c.Status != tc.status || !strings.Contains(c.Detail, tc.detail) || c.Hint != tc.hint {
				t.Errorf("%+v, want %s %q hint %q", c, tc.status, tc.detail, tc.hint)
			}
			if tc.status == Fail && c.Hint == "" {
				t.Error("every fail has a hint")
			}
		})
	}
}

// A set codex tier map drops the "optional" note (#68).
func TestCodexCheckTiersSet(t *testing.T) {
	f := &codexFake{fake: fake{have: map[string]bool{"codex": true}, reply: map[string]Result{"codex --version": {Stdout: "codex-cli 0.159.2\n"}}}}
	c := statusOf(Run(context.Background(), Input{CodexTiers: true, Exec: f.exec, Look: f.look}), "codex")
	if c.Status != Pass || strings.Contains(c.Detail, "round.tiers.codex") {
		t.Errorf("%+v", c)
	}
}

// codexFake records the CODEX_HOME each call ran with.
type codexFake struct {
	fake
	homes []string
}

func (f *codexFake) exec(ctx context.Context, bin string, args []string, env []string, dir string) (Result, error) {
	h := "-"
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "CODEX_HOME="); ok {
			h = v
		}
	}
	f.homes = append(f.homes, filepath.Base(bin)+" "+strings.Join(args, " ")+" @ "+h)
	return f.fake.exec(ctx, bin, args, env, dir)
}

// The version runs under the first slot home so ~/.codex is never read, and
// every home gets its own login call.
func TestCodexCheckUsesSlotHomes(t *testing.T) {
	f := &codexFake{fake: fake{
		have:  map[string]bool{"codex": true},
		reply: map[string]Result{"codex --version": {Stdout: "codex-cli 0.159.2"}, "codex login status": {}},
	}}
	homes := []CodexHome{{"ben", "/h/ben"}, {"dana", "/h/dana"}}
	if c := statusOf(Run(context.Background(), Input{CodexHomes: homes, Exec: f.exec, Look: f.look}), "codex"); c.Status != Pass {
		t.Fatalf("%+v", c)
	}
	want := "codex --version @ /h/ben|codex login status @ /h/ben|codex login status @ /h/dana"
	if got := strings.Join(f.homes, "|"); got != want {
		t.Errorf("calls %s, want %s", got, want)
	}
}

// orchFixture writes settings files under a project root and two config dirs.
type orchFixture struct {
	root, a, b string
}

func newOrch(t *testing.T) orchFixture {
	t.Helper()
	base := t.TempDir()
	return orchFixture{root: filepath.Join(base, "proj"), a: filepath.Join(base, "a"), b: filepath.Join(base, "b")}
}

func (o orchFixture) write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (o orchFixture) check(t *testing.T, have map[string]bool, name string) Check {
	t.Helper()
	f := &fake{have: have, reply: map[string]Result{}}
	r := Run(context.Background(), Input{Exec: f.exec, Look: f.look, ProjectRoot: o.root, ConfigDirs: []string{o.a, o.b}})
	return statusOf(r, name)
}

func TestOrchestratorChecksSkipOutsideProject(t *testing.T) {
	f := &fake{have: map[string]bool{}, reply: map[string]Result{}}
	r := Run(context.Background(), Input{Exec: f.exec, Look: f.look})
	for _, n := range []string{"statusline", "stop-hook"} {
		if c := statusOf(r, n); c.Status != Skip {
			t.Errorf("%s: %+v", n, c)
		}
	}
}

func TestStatuslineCheck(t *testing.T) {
	o := newOrch(t)
	if c := o.check(t, nil, "statusline"); c.Status != Skip {
		t.Fatalf("no settings file: %+v", c)
	}
	// Plain installed in the project, nothing user-level: both accounts see it.
	o.write(t, filepath.Join(o.root, ".claude", "settings.local.json"), `{"statusLine":{"type":"command","command":"rota statusline dump"}}`)
	if c := o.check(t, nil, "statusline"); c.Status != Pass {
		t.Fatalf("current: %+v", c)
	}
	// Wrapped counts as running the dump.
	o.write(t, filepath.Join(o.root, ".claude", "settings.local.json"), `{"statusLine":{"command":"rota statusline dump --then 'x'","rotaWrapped":"x"}}`)
	if c := o.check(t, nil, "statusline"); c.Status != Pass {
		t.Fatalf("wrapped: %+v", c)
	}
	// Hooks installed (so opted in) but no statusline dump: account a has its own
	// plain line, b has none. A partial install fails.
	o.write(t, filepath.Join(o.root, ".claude", "settings.local.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"rota hook stop # rota-hook"}]}]}}`)
	o.write(t, filepath.Join(o.a, "settings.json"), `{"statusLine":{"command":"~/bin/line.sh"}}`)
	c := o.check(t, nil, "statusline")
	if c.Status != Fail || c.Hint != "rota hook install --wrap-statusline" || !strings.Contains(c.Detail, o.a) || !strings.Contains(c.Detail, o.b+": no statusLine") {
		t.Fatalf("missing: %+v", c)
	}
	// A project-local entry outranks both user files.
	o.write(t, filepath.Join(o.root, ".claude", "settings.json"), `{"statusLine":{"command":"rota statusline dump"}}`)
	if c := o.check(t, nil, "statusline"); c.Status != Pass {
		t.Fatalf("project outranks user: %+v", c)
	}
	// An unparseable file fails and says which.
	o.write(t, filepath.Join(o.root, ".claude", "settings.local.json"), `{nope`)
	if c := o.check(t, nil, "statusline"); c.Status != Fail || !strings.Contains(c.Detail, "settings.local.json") {
		t.Fatalf("broken: %+v", c)
	}
}

func TestStopHookCheck(t *testing.T) {
	o := newOrch(t)
	// Nothing installed is not a fault: the hooks are opt-in.
	c := o.check(t, map[string]bool{"rota": true}, "stop-hook")
	if c.Status != Skip || !strings.Contains(c.Detail, "opt-in") || !strings.Contains(c.Detail, "rota hook install") {
		t.Fatalf("not installed: %+v", c)
	}
	// Only Stop installed.
	o.write(t, filepath.Join(o.a, "settings.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"rota hook stop # rota-hook"}]}]}}`)
	if c := o.check(t, map[string]bool{"rota": true}, "stop-hook"); c.Status != Fail || !strings.Contains(c.Detail, "SessionStart") || strings.Contains(c.Detail, "Stop and") {
		t.Fatalf("half: %+v", c)
	}
	// Both, spread over two scopes, resolving.
	o.write(t, filepath.Join(o.root, ".claude", "settings.local.json"), `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"rota hook session-start # rota-hook"}]}]}}`)
	if c := o.check(t, map[string]bool{"rota": true}, "stop-hook"); c.Status != Pass {
		t.Fatalf("current: %+v", c)
	}
	// Command that does not resolve.
	if c := o.check(t, nil, "stop-hook"); c.Status != Fail || !strings.Contains(c.Detail, "rota, which is not found") {
		t.Fatalf("unresolved: %+v", c)
	}
	// A user's own unmarked Stop hook is not ours.
	o2 := newOrch(t)
	o2.write(t, filepath.Join(o2.root, ".claude", "settings.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"rota hook stop"}]}]}}`)
	if c := o2.check(t, map[string]bool{"rota": true}, "stop-hook"); c.Status != Skip {
		t.Fatalf("unmarked: %+v", c)
	}
}

// A user's own statusline, with no rota entry anywhere, is not an install to
// check: both checks skip and say how to opt in. An unreadable settings file
// is named in the skip, since nothing could be ruled in or out there.
func TestOrchestratorChecksSkipUntilOptedIn(t *testing.T) {
	o := newOrch(t)
	o.write(t, filepath.Join(o.a, "settings.json"), `{"statusLine":{"command":"~/bin/line.sh"}}`)
	for _, n := range []string{"statusline", "stop-hook"} {
		if c := o.check(t, map[string]bool{"rota": true}, n); c.Status != Skip || !strings.Contains(c.Detail, "rota hook install") {
			t.Errorf("%s: %+v", n, c)
		}
	}
	o.write(t, filepath.Join(o.b, "settings.json"), `{nope`)
	if c := o.check(t, map[string]bool{"rota": true}, "stop-hook"); c.Status != Skip || !strings.Contains(c.Detail, "cannot read") {
		t.Errorf("unreadable: %+v", c)
	}
	// One marked entry anywhere opts in, and then the missing half fails.
	o.write(t, filepath.Join(o.root, ".claude", "settings.local.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"rota hook stop # rota-hook"}]}]}}`)
	if c := o.check(t, map[string]bool{"rota": true}, "stop-hook"); c.Status != Fail {
		t.Errorf("opted in: %+v", c)
	}
}

func TestSwitchCheck(t *testing.T) {
	o := newOrch(t)
	run := func(on bool, accts []Account, have map[string]bool) Check {
		f := &fake{have: have, reply: map[string]Result{}}
		r := Run(context.Background(), Input{Exec: f.exec, Look: f.look, ProjectRoot: o.root, ConfigDirs: []string{o.a, o.b}, SwitchOnUsage: on, Accounts: accts})
		return statusOf(r, "switch")
	}
	two := []Account{{"a", o.a}, {"b", o.b}}
	if c := run(false, nil, nil); c.Status != Skip {
		t.Fatalf("off: %+v", c)
	}
	if c := run(true, one(two), map[string]bool{"rota": true}); c.Status != Fail || c.Hint != "add a second account to work.accounts" {
		t.Fatalf("one account: %+v", c)
	}
	if c := run(true, []Account{{"a", o.a}, {"b", ""}}, nil); c.Status != Fail {
		t.Fatalf("no configDir does not count: %+v", c)
	}
	if c := run(true, two, map[string]bool{"rota": true}); c.Status != Fail || c.Hint != "rota hook install" {
		t.Fatalf("no hooks: %+v", c)
	}
	o.write(t, filepath.Join(o.root, ".claude", "settings.local.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"rota hook stop # rota-hook"}]}],"SessionStart":[{"hooks":[{"type":"command","command":"rota hook session-start # rota-hook"}]}]}}`)
	if c := run(true, two, map[string]bool{"rota": true}); c.Status != Pass {
		t.Fatalf("ready: %+v", c)
	}
}

func one(a []Account) []Account { return a[:1] }
