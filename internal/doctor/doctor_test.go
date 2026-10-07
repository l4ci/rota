package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/stalebin"
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

func TestRunTable(t *testing.T) {
	home := t.TempDir()
	good := filepath.Join(home, "good")
	os.MkdirAll(good, 0o755)
	os.WriteFile(filepath.Join(good, ".credentials.json"), []byte("{}"), 0o600)
	nocred := filepath.Join(home, "nocred")
	os.MkdirAll(nocred, 0o755)
	cur, miss := fixture(t, "integration_status_current.txt"), fixture(t, "integration_status_missing.txt")

	all := map[string]bool{"git": true, "jq": true, "herdr": true, "tmux": true, "gh": true, "glab": true}
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
	herdrIn := Input{Dispatch: "herdr", Accounts: []config.Account{{Name: "a", ConfigDir: good}}}
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
	inHerdr := Input{Dispatch: "subagent", Getenv: env("HERDR_ENV", "1"), Accounts: []config.Account{{Name: "a", ConfigDir: good}}}

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
		{"jq ok", Input{}, all, with(nil), "jq", Pass, "on PATH", ""},
		{"jq missing", Input{}, without("jq"), with(nil), "jq", Fail, "jq not found", "install jq"},
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
		{"accounts ok", Input{Accounts: []config.Account{{Name: "a", ConfigDir: good}}}, all, with(nil), "accounts", Pass, "1 accounts", ""},
		{"accounts tilde", Input{Home: home, Accounts: []config.Account{{Name: "a", ConfigDir: "~/good"}}}, all, with(nil), "accounts", Pass, "", ""},
		{"accounts no dir", Input{Accounts: []config.Account{{Name: "a", ConfigDir: filepath.Join(home, "gone")}}}, all, with(nil), "accounts", Fail, "does not exist", "claude /login"},
		{"accounts no creds", Input{Accounts: []config.Account{{Name: "a", ConfigDir: nocred}}}, all, with(nil), "accounts", Fail, "no credentials file", "CLAUDE_CONFIG_DIR="},
		{"accounts no configDir", Input{Accounts: []config.Account{{Name: "a", ConfigDir: ""}}}, all, with(nil), "accounts", Fail, "no configDir", "work.accounts"},

		{"hook ok", herdrIn, all, with(nil), "hook", Pass, "a: current", ""},
		{"hook not installed", herdrIn, all, with(map[string]Result{"herdr integration status": {Stdout: miss}}), "hook", Fail, "a: not installed", "herdr integration install claude"},
		{"hook status errors", herdrIn, all, with(map[string]Result{"herdr integration status": {ExitCode: 1}}), "hook", Fail, "status failed", "herdr integration install claude"},
		{"hook runs for a detected herdr pane", inHerdr, all, with(nil), "hook", Pass, "a: current", ""},
		{"hook skips for detected tmux", Input{Dispatch: "subagent", Getenv: env("TMUX", "x"), Accounts: []config.Account{{Name: "a", ConfigDir: good}}}, all, with(nil), "hook", Skip, "not herdr", ""},
		{"hook skips off herdr", Input{Dispatch: "tmux", Accounts: []config.Account{{Name: "a", ConfigDir: good}}}, all, with(nil), "hook", Skip, "not herdr", ""},
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
	in := Input{Dispatch: "herdr", Home: d, Accounts: []config.Account{{Name: "a", ConfigDir: "~/one"}, {Name: "b", ConfigDir: "/two"}}, Exec: f.exec, Look: f.look}
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
	if got := strings.Join(names, ","); got != "git,jq,host,tracker,accounts,hook,statusline,stop-hook,switch,skills,codex" {
		t.Errorf("order %s", got)
	}
	if r.OK() {
		t.Error("git is missing, so the report is not OK")
	}
	if !(Report{Checks: []Check{{Status: Pass}, {Status: Skip}}}).OK() {
		t.Error("pass and skip are OK")
	}
}

func TestDiskCheck(t *testing.T) {
	const gib = 1 << 30
	left := []string{"3 leaked temp dirs under /tmp (1.2 GiB)"}
	for _, tc := range []struct {
		name   string
		in     Input
		status string // "" = no disk line
		detail string
		hint   string
	}{
		{"low free space warns", Input{Disk: &Disk{Path: "/tmp", Free: 3 * gib, Total: 75 * gib}, MinFreeDiskPercent: 10, Leftovers: left}, Warn, "3.0 GiB free (4%), under the 10% threshold", "3 leaked temp dirs"},
		{"low space with nothing to reclaim still warns", Input{Disk: &Disk{Path: "/tmp", Free: 3 * gib, Total: 75 * gib}, MinFreeDiskPercent: 10}, Warn, "/tmp has", "doctor.minFreeDiskPercent"},
		{"enough space is silent", Input{Disk: &Disk{Path: "/tmp", Free: 30 * gib, Total: 75 * gib}, MinFreeDiskPercent: 10, Leftovers: left}, "", "", ""},
		{"threshold 0 turns it off", Input{Disk: &Disk{Path: "/tmp", Free: 1, Total: 75 * gib}}, "", "", ""},
		{"unreadable disk is silent", Input{MinFreeDiskPercent: 10}, "", "", ""},
	} {
		in := tc.in
		f := &fake{have: map[string]bool{}, reply: map[string]Result{}}
		in.Exec, in.Look = f.exec, f.look
		var got *Check
		rep := Run(context.Background(), in)
		for i := range rep.Checks {
			if rep.Checks[i].Name == "disk" {
				got = &rep.Checks[i]
			}
		}
		if tc.status == "" {
			if got != nil {
				t.Errorf("%s: unexpected disk check %+v", tc.name, *got)
			}
			continue
		}
		if got == nil || got.Status != tc.status || !strings.Contains(got.Detail, tc.detail) || !strings.Contains(got.Hint, tc.hint) {
			t.Errorf("%s: got %+v", tc.name, got)
		}
	}
	if !(Report{Checks: []Check{{Status: Warn}}}).OK() {
		t.Error("a warning does not fail the report")
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

func TestCodexCheck(t *testing.T) {
	// the claude fixtures carry a codex line of their own, so build these here
	codexCur := "claude: current (v10)\ncodex: current (v8) (/x)\n"
	codexMiss := "claude: current (v10)\ncodex: not installed (/x)\n"
	homes := []CodexHome{{"ben", "/h/ben"}, {"dana", "/h/dana"}}
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
		{"skip: no codex, default home", nil, "herdr", []CodexHome{{}}, nil, Skip, "no work.codexAccounts configured", ""},
		{"pass: codex alone", []string{"codex"}, "", nil, map[string]Result{"codex --version": ver}, Pass, "codex 0.159.2; round.tiers.codex unset (optional)", ""},
		{"pass: default home logged in", []string{"codex", "herdr"}, "herdr", []CodexHome{{}},
			map[string]Result{"codex --version": ver, "codex login status": {}, "herdr integration status": {Stdout: codexCur}},
			Pass, "homes checked: default home", ""},
		{"pass with a note: default home not logged in", []string{"codex"}, "", []CodexHome{{}},
			map[string]Result{"codex --version": ver, "codex login status": {ExitCode: 1}},
			Pass, "default home: not logged in (run `codex login` before a Codex worker)", ""},
		{"fail: accounts but no codex", nil, "herdr", homes, nil, Fail, "codex not found on PATH", harness.CodexInstallHint},
		{"pass: version not in the output", []string{"codex"}, "", nil, map[string]Result{"codex --version": {Stdout: "hello"}}, Pass, "codex; round.tiers.codex unset", ""},
		{"fail: version command fails", []string{"codex"}, "", nil, map[string]Result{"codex --version": {ExitCode: 3, Stdout: "codex-cli 0.159.2"}}, Fail, "not runnable", harness.CodexInstallHint},
		{"pass: a newer codex", []string{"codex"}, "", nil, map[string]Result{"codex --version": {Stdout: "codex-cli 0.160.1"}}, Pass, "codex 0.160.1", ""},
		{"pass: logged in, herdr integration current", []string{"codex", "herdr"}, "herdr", homes,
			map[string]Result{"codex --version": ver, "codex login status": {}, "herdr integration status": {Stdout: codexCur}},
			Pass, "homes checked: ben, dana; round.tiers.codex unset (optional)", ""},
		{"pass: tmux skips the integration", []string{"codex", "herdr"}, "tmux", homes,
			map[string]Result{"codex --version": ver, "codex login status": {}, "herdr integration status": {Stdout: codexMiss}},
			Pass, "ben, dana", ""},
		{"fail: not logged in", []string{"codex", "herdr"}, "herdr", homes,
			map[string]Result{"codex --version": ver, "codex login status": {ExitCode: 1}, "herdr integration status": {Stdout: codexCur}},
			Fail, "ben: not logged in", "CODEX_HOME=/h/ben codex login"},
		{"fail: integration missing", []string{"codex", "herdr"}, "herdr", homes[:1],
			map[string]Result{"codex --version": ver, "codex login status": {}, "herdr integration status": {Stdout: codexMiss}},
			Fail, "ben: herdr integration not current", "CODEX_HOME=/h/ben herdr integration install codex"},
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

// The version runs under the first account's home, and every configured
// account gets its own login call.
func TestCodexCheckUsesAccountHomes(t *testing.T) {
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
	run := func(on bool, accts []config.Account, have map[string]bool) Check {
		f := &fake{have: have, reply: map[string]Result{}}
		r := Run(context.Background(), Input{Exec: f.exec, Look: f.look, ProjectRoot: o.root, ConfigDirs: []string{o.a, o.b}, SwitchOnUsage: on, Accounts: accts})
		return statusOf(r, "switch")
	}
	two := []config.Account{{Name: "a", ConfigDir: o.a}, {Name: "b", ConfigDir: o.b}}
	if c := run(false, nil, nil); c.Status != Skip {
		t.Fatalf("off: %+v", c)
	}
	if c := run(true, one(two), map[string]bool{"rota": true}); c.Status != Fail || c.Hint != "add a second account to work.accounts" {
		t.Fatalf("one account: %+v", c)
	}
	if c := run(true, []config.Account{{Name: "a", ConfigDir: o.a}, {Name: "b", ConfigDir: ""}}, nil); c.Status != Fail {
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

func one(a []config.Account) []config.Account { return a[:1] }

// The herdr pin lives in host.SupportedHerdr: doctor's verdict follows it.
func TestHostFollowsSupportedHerdr(t *testing.T) {
	old := host.SupportedHerdr
	t.Cleanup(func() { host.SupportedHerdr = old })
	host.SupportedHerdr = "0.10"
	f := &fake{
		have:  map[string]bool{"herdr": true},
		reply: map[string]Result{"herdr --version": {Stdout: "herdr 0.10.1\n"}},
	}
	in := Input{Dispatch: "herdr", Exec: f.exec, Look: f.look}
	if c := statusOf(Run(context.Background(), in), "host"); c.Status != Pass || !strings.Contains(c.Detail, "0.10.1") {
		t.Errorf("0.10.1 under pin 0.10: %+v", c)
	}
	f.reply["herdr --version"] = Result{Stdout: "herdr 0.9.3\n"}
	c := statusOf(Run(context.Background(), in), "host")
	if c.Status != Fail || !strings.Contains(c.Detail, "need 0.10.x") || !strings.Contains(c.Hint, "0.10.x") {
		t.Errorf("0.9.3 under pin 0.10: %+v", c)
	}
}

func TestBinaryCheck(t *testing.T) {
	f := &fake{have: map[string]bool{}, reply: map[string]Result{}}
	find := func(in Input) *Check {
		in.Exec, in.Look = f.exec, f.look
		rep := Run(context.Background(), in)
		for i := range rep.Checks {
			if rep.Checks[i].Name == "binary" {
				return &rep.Checks[i]
			}
		}
		return nil
	}
	if c := find(Input{}); c != nil {
		t.Errorf("no finding must add no line: %+v", c)
	}
	c := find(Input{StaleBinary: &stalebin.Finding{Commit: "aaaaaaa", Head: "bbbbbbb", Behind: 2, Rebuild: "go build ./cmd/rota"}})
	if c == nil || c.Status != Warn || !strings.Contains(c.Detail, "aaaaaaa") || !strings.Contains(c.Hint, "go build ./cmd/rota") {
		t.Errorf("binary check = %+v", c)
	}
}
