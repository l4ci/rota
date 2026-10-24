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
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/stalebin"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
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
	os.WriteFile(filepath.Join(good, ".credentials.json"), []byte(`{"claudeAiOauth":{"accessToken":"t"}}`), 0o600)
	notoken := filepath.Join(home, "notoken")
	os.MkdirAll(notoken, 0o755)
	os.WriteFile(filepath.Join(notoken, ".credentials.json"), []byte("{}"), 0o600)
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
	noRemote := map[string]Result{"git remote get-url origin": {ExitCode: 2}}
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
	herdrIn := Input{Dispatch: "herdr", RoundHost: host.Herdr, Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: good}}}
	inHerdr := Input{Dispatch: "subagent", RoundHost: host.Herdr, Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: good}}}

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
		{"host tmux ok", Input{Dispatch: "tmux", RoundHost: host.Tmux}, all, with(nil), "host", Pass, "tmux", ""},
		{"host tmux missing", Input{Dispatch: "tmux", RoundHost: host.Tmux}, without("tmux"), with(nil), "host", Fail, "tmux not found", "install tmux"},

		{"host detects herdr in a pane", inHerdr, all, with(nil), "host", Pass, "herdr 0.9.3", ""},
		{"host detects herdr when dispatch is unset", Input{RoundHost: host.Herdr}, all, with(nil), "host", Pass, "herdr 0.9.3", ""},
		{"host reads the recorded round host with no pane env", Input{Dispatch: "subagent", RoundHost: host.Herdr}, all, with(nil), "host", Pass, "herdr 0.9.3", ""},
		{"host recorded solo beats a tmux pane", Input{Dispatch: "tmux", RegistrySolo: true, RoundHost: host.Solo}, all, with(nil), "host", Skip, "solo", ""},

		{"tracker github ok", Input{}, all, with(nil), "tracker", Pass, "gh authenticated", ""},
		{"tracker gh unauthenticated", Input{}, all, with(map[string]Result{"gh auth status": {ExitCode: 1}}), "tracker", Fail, "not authenticated", "gh auth login"},
		{"tracker gh missing", Input{}, without("gh"), with(nil), "tracker", Fail, "gh not found", ""},
		{"tracker gitlab", Input{}, all, with(map[string]Result{"git remote get-url origin": {Stdout: "https://gitlab.com/a/b"}}), "tracker", Pass, "glab", ""},
		{"tracker no remote", Input{}, all, with(map[string]Result{"git remote get-url origin": {ExitCode: 2}}), "tracker", Skip, "no origin", ""},
		{"tracker config fallback", Input{Tracker: tracker.Settings{Provider: "gitlab"}}, all, with(map[string]Result{"git remote get-url origin": {ExitCode: 2}}), "tracker", Pass, "glab", ""},

		{"tracker unknown host assumes github", Input{}, all, with(map[string]Result{"git remote get-url origin": {Stdout: "https://git.example.com/a/b"}}), "tracker", Pass, "gh authenticated (forge github (assumed: origin https://git.example.com/a/b names no forge))", ""},

		// the tracker line reports the forge and the issue source (#797)
		{"tracker github origin, file issues", Input{Tracker: tracker.Settings{Source: "file"}}, all, with(nil), "tracker", Pass, "gh authenticated (forge github (from origin), issues file)", ""},
		{"tracker no remote is local", Input{Tracker: tracker.Settings{Source: "file"}}, all, with(noRemote), "tracker", Skip, "forge local (no origin remote), issues file", ""},
		{"tracker no remote and github source fails", Input{Tracker: tracker.Settings{Source: "github"}}, all, with(noRemote), "tracker", Fail, "issues.source is github but the forge is local", "forge.provider github"},
		{"tracker gitlab origin and github source fails", Input{Tracker: tracker.Settings{Source: "github"}}, all, with(map[string]Result{"git remote get-url origin": {Stdout: "https://gitlab.com/a/b"}}), "tracker", Fail, "issues.source is github but the forge is gitlab", "issues.source gitlab"},
		{"tracker matching github source", Input{Tracker: tracker.Settings{Source: "github"}}, all, with(nil), "tracker", Pass, "forge github (from origin), issues github", ""},
		{"tracker matching gitlab source", Input{Tracker: tracker.Settings{Source: "gitlab"}}, all, with(map[string]Result{"git remote get-url origin": {Stdout: "git@gitlab.example.org:a/b.git"}}), "tracker", Pass, "issues gitlab", ""},
		{"tracker explicit provider beats a silent origin", Input{Tracker: tracker.Settings{Source: "github", Provider: "github"}}, all, with(noRemote), "tracker", Pass, "forge github (forge.provider), issues github", ""},
		{"tracker explicit local", Input{Tracker: tracker.Settings{Source: "file", Provider: "local"}}, all, with(nil), "tracker", Skip, "forge local (forge.provider)", ""},
		{"tracker jira issues on a github forge", Input{Tracker: tracker.Settings{Source: "jira", JiraProject: "PROJ", Connection: "work"}}, all, with(nil), "tracker", Pass, "forge github (from origin), issues jira", ""},
		{"tracker jira without a project fails", Input{Tracker: tracker.Settings{Source: "jira", Connection: "work"}}, all, with(nil), "tracker", Fail, "issues.jira.project is not set", "rota config set issues.jira.project"},
		{"tracker jira without a connection fails", Input{Tracker: tracker.Settings{Source: "jira", JiraProject: "PROJ"}}, all, with(nil), "tracker", Fail, "issues.connection is not set", "rota connect add"},
		{"tracker linear issues on a github forge", Input{Tracker: tracker.Settings{Source: "linear", LinearTeam: "ENG", Connection: "work"}}, all, with(nil), "tracker", Pass, "forge github (from origin), issues linear", ""},
		{"tracker linear without a team fails", Input{Tracker: tracker.Settings{Source: "linear", Connection: "work"}}, all, with(nil), "tracker", Fail, "issues.linear.team is not set", "rota config set issues.linear.team"},
		{"tracker linear without a connection fails", Input{Tracker: tracker.Settings{Source: "linear", LinearTeam: "ENG"}}, all, with(nil), "tracker", Fail, "issues.connection is not set", "--kind linear"},
		{"tracker linear team the key cannot see fails", Input{TeamProblem: "Linear team NOPE is not visible to this API key", Tracker: tracker.Settings{Source: "linear", LinearTeam: "NOPE", Connection: "work"}}, all, with(nil), "tracker", Fail, "team NOPE is not visible", "issues.linear.team"},
		{"tracker itsaplan issues on a github forge", Input{Tracker: tracker.Settings{Source: "itsaplan", ItsaplanProject: "MKT", Connection: "plan"}}, all, with(nil), "tracker", Pass, "forge github (from origin), issues itsaplan", ""},
		{"tracker itsaplan without a project fails", Input{Tracker: tracker.Settings{Source: "itsaplan", Connection: "plan"}}, all, with(nil), "tracker", Fail, "issues.itsaplan.project is not set", "rota config set issues.itsaplan.project"},
		{"tracker itsaplan without a connection fails", Input{Tracker: tracker.Settings{Source: "itsaplan", ItsaplanProject: "MKT"}}, all, with(nil), "tracker", Fail, "issues.connection is not set", "--kind itsaplan"},
		{"tracker itsaplan project the key cannot see fails", Input{TeamProblem: "It's a Plan project NOPE is not visible to this API key", Tracker: tracker.Settings{Source: "itsaplan", ItsaplanProject: "NOPE", Connection: "plan"}}, all, with(nil), "tracker", Fail, "project NOPE is not visible", "issues.itsaplan.project"},
		{"tracker unknown host and gitlab source fails", Input{Tracker: tracker.Settings{Source: "gitlab"}}, all, with(map[string]Result{"git remote get-url origin": {Stdout: "https://git.example.com/a/b"}}), "tracker", Fail, "the forge is github: forge github (assumed: origin", "forge.provider gitlab"},
		{"tracker forge source needs no match", Input{Tracker: tracker.Settings{Source: "forge"}}, all, with(map[string]Result{"git remote get-url origin": {Stdout: "https://gitlab.com/a/b"}}), "tracker", Pass, "issues forge", ""},
		{"tracker legacy keys are named", Input{Tracker: tracker.Settings{Source: "issues"}}, all, with(nil), "tracker", Pass, "rota migrate source", ""},
		{"tracker invalid config fails", Input{SourceProblem: "invalid issues.source 'svn'"}, all, with(nil), "tracker", Fail, "invalid issues.source", "rota config set"},
		{"tracker outside a project says the forge", Input{}, all, with(noRemote), "tracker", Skip, "forge local", ""},

		{"accounts none", Input{}, all, with(nil), "accounts", Skip, "no accounts", ""},
		{"accounts ok", Input{Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: good}}}, all, with(nil), "accounts", Pass, "1 accounts", ""},
		{"accounts tilde", Input{Home: home, Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: "~/good"}}}, all, with(nil), "accounts", Pass, "", ""},
		{"accounts no dir", Input{Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: filepath.Join(home, "gone")}}}, all, with(nil), "accounts", Fail, "does not exist", "claude /login"},
		{"accounts no creds", Input{Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: nocred}}}, all, with(nil), "accounts", Fail, "no credentials file", "CLAUDE_CONFIG_DIR="},
		{"accounts no access token", Input{Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: notoken}}}, all, with(nil), "accounts", Fail, "no access token", "CLAUDE_CONFIG_DIR="},
		{"accounts no configDir", Input{Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: ""}}}, all, with(nil), "accounts", Fail, "no dir", "round.accounts"},

		{"hook ok", herdrIn, all, with(nil), "hook", Pass, "a: current", ""},
		{"hook not installed", herdrIn, all, with(map[string]Result{"herdr integration status": {Stdout: miss}}), "hook", Fail, "a: not installed", "herdr integration install claude"},
		{"hook status errors", herdrIn, all, with(map[string]Result{"herdr integration status": {ExitCode: 1}}), "hook", Fail, "status failed", "herdr integration install claude"},
		{"hook runs for a detected herdr pane", inHerdr, all, with(nil), "hook", Pass, "a: current", ""},
		{"hook skips for detected tmux", Input{Dispatch: "subagent", RoundHost: host.Tmux, Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: good}}}, all, with(nil), "hook", Skip, "not herdr", ""},
		{"hook skips off herdr", Input{Dispatch: "tmux", Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: good}}}, all, with(nil), "hook", Skip, "not herdr", ""},
		{"hook skips without accounts", Input{Dispatch: "herdr", RoundHost: host.Herdr}, all, with(nil), "hook", Skip, "no accounts", ""},
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
	in := Input{Dispatch: "herdr", RoundHost: host.Herdr, Home: d, Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: "~/one"}, {Kind: config.KindClaude, Name: "b", Dir: "/two"}}, Exec: f.exec, Look: f.look}
	c := statusOf(Run(context.Background(), in), "hook")
	if c.Status != Pass {
		t.Fatalf("%+v", c)
	}
	want := []string{filepath.Join(d, "one"), "/two"}
	if strings.Join(f.envs, "|") != strings.Join(want, "|") {
		t.Errorf("config dirs %v, want %v", f.envs, want)
	}
}

func TestAccountsWarnsWhenTheOrchestratorDirIsNotListed(t *testing.T) {
	f := &fake{}
	in := Input{Home: t.TempDir(), OrchestratorConfigDir: "/orch", Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: "/two"}}, Exec: f.exec, Look: f.look}
	c := statusOf(Run(context.Background(), in), "accounts")
	if c.Status != Warn || !strings.Contains(c.Detail, "/orch") {
		t.Fatalf("%+v", c)
	}
	in.Accounts = nil
	if c := statusOf(Run(context.Background(), in), "accounts"); c.Status != Skip {
		t.Fatalf("%+v", c)
	}
}

func TestOrderAndOK(t *testing.T) {
	f := &fake{have: map[string]bool{}, reply: map[string]Result{}}
	r := Run(context.Background(), Input{Exec: f.exec, Look: f.look})
	var names []string
	for _, c := range r.Checks {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "git,jq,host,tracker,accounts,hook,statusline,stop-hook,guard-hook,switch,skills,codex,opencode" {
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
	plugin := func(mod func(*skills.RootStatus)) skills.RootStatus {
		return root(func(r *skills.RootStatus) {
			r.Path, r.Version, r.Plugin = "/h/.claude/plugins/cache/rota/rota/5.0.0/skills", "5.0.0", true
			if mod != nil {
				mod(r)
			}
		})
	}
	const pluginHint = "run /rota:rota-install in Claude Code (binary to the plugin's version) or claude plugin update rota@rota"
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
		{"stub current after a body-only release", rep(root(func(r *skills.RootStatus) { r.Mode, r.Version = "stub", "4.9.0" })), Pass, "match rota 5.0.0", ""},
		{"stub stale names the skill", rep(root(func(r *skills.RootStatus) {
			r.Mode, r.Current, r.Changed = "stub", false, []string{"rota-new (added)", "rota-work (stub changed)"}
		})), Fail, "changed: rota-new (added), rota-work (stub changed)", "run: rota skills update"},
		{"edited", rep(root(func(r *skills.RootStatus) { r.Edited = []string{"rota-work/SKILL.md"} })), Fail, "1 edited (rota-work/SKILL.md)", "run: rota skills update --overwrite"},
		{"missing", rep(root(func(r *skills.RootStatus) { r.Missing = []string{"rota-work/SKILL.md", "rota-ship/SKILL.md"} })), Fail, "2 missing", "run: rota skills update"},
		{"second root only", rep(root(func(r *skills.RootStatus) { r.Installed = false }), root(func(r *skills.RootStatus) { r.Path = "/h/.agents/skills" })), Pass, "1 roots", ""},
		{"plugin current", rep(plugin(nil)), Pass, "1 roots match rota 5.0.0", ""},
		{"plugin stale", rep(plugin(func(r *skills.RootStatus) { r.Digest, r.Current = "bbbb", false })), Fail, "rota/5.0.0/skills (plugin)", pluginHint},
		{"plugin missing", rep(plugin(func(r *skills.RootStatus) { r.Missing = []string{"rota-work/SKILL.md"} })), Fail, "(plugin): 1 missing", pluginHint},
		{"plugin and manifest stale", rep(root(func(r *skills.RootStatus) { r.Current = false }), plugin(func(r *skills.RootStatus) { r.Current = false })), Fail, "(plugin)", "run: rota skills update; " + pluginHint},
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

func TestSkillsCheckPluginExtras(t *testing.T) {
	const bin = "aaaaaaaaaaaaaaaa"
	f := &fake{have: map[string]bool{}}
	manifest := skills.RootStatus{Root: skills.Root{Path: "/h/.claude/skills", Agent: "claude", Scope: "user"}, Installed: true, Version: "5.0.0", Digest: bin, Current: true}
	plugin := skills.RootStatus{Root: skills.Root{Path: "/p/skills", Agent: "claude", Scope: "user", Plugin: true}}
	run := func(rep *skills.Report) Check {
		return statusOf(Run(context.Background(), Input{Skills: rep, Exec: f.exec, Look: f.look}), "skills")
	}
	warns := []string{"read /p/installed_plugins.json: permission denied"}
	if c := run(&skills.Report{Version: "5.0.0", Digest: bin, Roots: []skills.RootStatus{manifest}, Warnings: warns}); c.Status != Warn || !strings.Contains(c.Detail, "permission denied") || !strings.Contains(c.Detail, "1 roots match") || c.Hint == "" {
		t.Errorf("warning beside a passing root: %+v", c)
	}
	stale := manifest
	stale.Current, stale.Digest = false, "bbbb"
	if c := run(&skills.Report{Version: "5.0.0", Digest: bin, Roots: []skills.RootStatus{stale}, Warnings: warns}); c.Status != Fail || !strings.Contains(c.Detail, "permission denied") || c.Hint != "run: rota skills update" {
		t.Errorf("warning beside a failing root: %+v", c)
	}
	if c := run(&skills.Report{Version: "5.0.0", Digest: bin, Roots: []skills.RootStatus{plugin}}); c.Status != Skip || !strings.Contains(c.Detail, "claude plugin update rota@rota") || strings.Contains(c.Detail, "rota skills install") {
		t.Errorf("plugin recorded, dir missing: %+v", c)
	}
	if c := run(&skills.Report{Version: "5.0.0", Digest: bin, Roots: []skills.RootStatus{{Root: skills.Root{Path: "/h/.claude/skills"}}}}); c.Status != Skip || !strings.Contains(c.Detail, "rota skills install") {
		t.Errorf("nothing recorded: %+v", c)
	}
}

func TestCodexCheck(t *testing.T) {
	// the claude fixtures carry a codex line of their own, so build these here
	codexCur := "claude: current (v10)\ncodex: current (v8) (/x)\n"
	codexMiss := "claude: current (v10)\ncodex: not installed (/x)\n"
	homes := []Home{{"ben", "/h/ben"}, {"dana", "/h/dana"}}
	ver := Result{Stdout: "codex-cli 0.159.2\n"}
	for _, tc := range []struct {
		name     string
		have     []string
		dispatch string
		homes    []Home
		reply    map[string]Result
		status   string
		detail   string // substring
		hint     string
	}{
		{"skip: no codex, default home", nil, "herdr", []Home{{}}, nil, Skip, "no codex account in round.accounts", ""},
		{"pass: codex alone", []string{"codex"}, "", nil, map[string]Result{"codex --version": ver}, Pass, "codex 0.159.2; round.tiers.codex unset (optional)", ""},
		{"pass: default home logged in", []string{"codex", "herdr"}, "herdr", []Home{{}},
			map[string]Result{"codex --version": ver, "codex login status": {}, "herdr integration status": {Stdout: codexCur}},
			Pass, "homes checked: default home", ""},
		{"pass with a note: default home not logged in", []string{"codex"}, "", []Home{{}},
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
			c := statusOf(Run(context.Background(), Input{Dispatch: tc.dispatch, Kinds: codexIn(tc.homes, false), Exec: f.exec, Look: f.look}), "codex")
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
	c := statusOf(Run(context.Background(), Input{Kinds: codexIn([]Home{{}}, true), Exec: f.exec, Look: f.look}), "codex")
	if c.Status != Pass || strings.Contains(c.Detail, "round.tiers.codex") {
		t.Errorf("%+v", c)
	}
}

// With no tier set, the note names the models a worker starts with: the
// shipped defaults, or Codex's own default once all three are emptied (#1602).
func TestCodexCheckUnsetTiersNamesEffectiveModels(t *testing.T) {
	for _, tc := range []struct {
		name   string
		models []string
		want   string
		not    string
	}{
		{"shipped defaults", []string{"light=gpt-6-luna", "standard=gpt-6.1-sol", "heavy=gpt-6-astra"}, "workers use light=gpt-6-luna, standard=gpt-6.1-sol, heavy=gpt-6-astra", "default model"},
		{"emptied", nil, "workers use Codex's default model", "gpt-6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &codexFake{fake: fake{have: map[string]bool{"codex": true}, reply: map[string]Result{"codex --version": {Stdout: "codex-cli 0.159.2\n"}}}}
			in := codexIn([]Home{{}}, false)
			k := in[harness.Codex]
			k.Models = tc.models
			in[harness.Codex] = k
			c := statusOf(Run(context.Background(), Input{Kinds: in, Exec: f.exec, Look: f.look}), "codex")
			if c.Status != Pass || !strings.Contains(c.Detail, tc.want) || strings.Contains(c.Detail, tc.not) {
				t.Errorf("%+v", c)
			}
		})
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
	homes := []Home{{"ben", "/h/ben"}, {"dana", "/h/dana"}}
	if c := statusOf(Run(context.Background(), Input{Kinds: codexIn(homes, false), Exec: f.exec, Look: f.look}), "codex"); c.Status != Pass {
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

func TestGuardHookCheck(t *testing.T) {
	guard := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"rota hook guard # rota-hook"}]}]}}`
	have := map[string]bool{"rota": true}
	o := newOrch(t)
	if c := o.check(t, have, "guard-hook"); c.Status != Skip || !strings.Contains(c.Detail, "opt-in") {
		t.Fatalf("not installed: %+v", c)
	}
	// Opted in through the Stop hook, but no guard (an install from before #702).
	o.write(t, filepath.Join(o.a, "settings.json"), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"rota hook stop # rota-hook"}]}]}}`)
	if c := o.check(t, have, "guard-hook"); c.Status != Fail || !strings.Contains(c.Hint, "rota hook install --scope") {
		t.Fatalf("missing: %+v", c)
	}
	// Project-local alone does not reach a worker worktree.
	o.write(t, filepath.Join(o.root, ".claude", "settings.local.json"), guard)
	if c := o.check(t, have, "guard-hook"); c.Status != Fail || !strings.Contains(c.Detail, "project-local") {
		t.Fatalf("local only: %+v", c)
	}
	// Project scope counts.
	o.write(t, filepath.Join(o.root, ".claude", "settings.json"), guard)
	if c := o.check(t, have, "guard-hook"); c.Status != Pass {
		t.Fatalf("project: %+v", c)
	}
	if c := o.check(t, nil, "guard-hook"); c.Status != Fail || !strings.Contains(c.Detail, "not found") {
		t.Fatalf("unresolved: %+v", c)
	}
	// User scope counts too.
	o2 := newOrch(t)
	o2.write(t, filepath.Join(o2.b, "settings.json"), guard)
	if c := o2.check(t, have, "guard-hook"); c.Status != Pass {
		t.Fatalf("user: %+v", c)
	}
	// Codex workers are not guarded: warn.
	f := &fake{have: have, reply: map[string]Result{}}
	r := Run(context.Background(), Input{Exec: f.exec, Look: f.look, ProjectRoot: o2.root, ConfigDirs: []string{o2.a, o2.b}, Kinds: codexIn([]Home{{}}, true)})
	if c := statusOf(r, "guard-hook"); c.Status != Warn || !strings.Contains(c.Detail, "Codex") {
		t.Fatalf("codex: %+v", c)
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
	two := []config.Account{{Kind: config.KindClaude, Name: "a", Dir: o.a}, {Kind: config.KindClaude, Name: "b", Dir: o.b}}
	if c := run(false, nil, nil); c.Status != Skip {
		t.Fatalf("off: %+v", c)
	}
	if c := run(true, one(two), map[string]bool{"rota": true}); c.Status != Fail || c.Hint != "add a second account to round.accounts" {
		t.Fatalf("one account: %+v", c)
	}
	if c := run(true, []config.Account{{Kind: config.KindClaude, Name: "a", Dir: o.a}, {Kind: config.KindClaude, Name: "b", Dir: ""}}, nil); c.Status != Fail {
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
	in := Input{Dispatch: "herdr", RoundHost: host.Herdr, Exec: f.exec, Look: f.look}
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

func TestAgentsCheck(t *testing.T) {
	find := func(in Input) Check {
		in.Look = func(string) (string, bool) { return "", false }
		return statusOf(Run(context.Background(), in), "agents")
	}
	if c := find(Input{}); c.Name != "" {
		t.Fatalf("no problems adds no line: %+v", c)
	}
	c := find(Input{AgentProblems: []string{".claude/agents/rota-explorer.md: missing"}})
	if c.Status != Warn || !strings.Contains(c.Detail, "rota-explorer.md: missing") || !strings.Contains(c.Hint, "rota agents write") {
		t.Fatalf("%+v", c)
	}
}

func TestGitignoreCheck(t *testing.T) {
	find := func(in Input) Check {
		in.Look = func(string) (string, bool) { return "", false }
		for _, c := range Run(context.Background(), in).Checks {
			if c.Name == "gitignore" {
				return c
			}
		}
		return Check{}
	}
	if c := find(Input{}); c.Name != "" {
		t.Fatalf("nothing missing adds no line: %+v", c)
	}
	c := find(Input{GitignoreMissing: []string{".rota/**/knowledge-hits.jsonl"}})
	if c.Status != Warn || !strings.Contains(c.Detail, "knowledge-hits.jsonl") || !strings.Contains(c.Hint, "rota init") {
		t.Fatalf("%+v", c)
	}
}

func portsInput(f *fake, cwd map[int]string) Input {
	return Input{
		Slots: []SlotBlock{{Name: "w1", Worktree: "/p/.worktrees/w1", Base: 20000, Size: 100}, {Name: "w2", Worktree: "/p/.worktrees/w2", Base: 20100, Size: 100}},
		CwdOf: func(pid int) string { return cwd[pid] },
		Exec:  f.exec, Look: f.look,
	}
}

const ssOut = `LISTEN 0 511 127.0.0.1:20042 0.0.0.0:* users:(("node",pid=77,fd=19))
LISTEN 0 511 *:20150 *:* users:(("node",pid=78,fd=19))
LISTEN 0 128 0.0.0.0:22 0.0.0.0:* users:(("sshd",pid=5,fd=3))
`

func TestPortsWarnsOnForeignListenerInASlotBlock(t *testing.T) {
	f := &fake{have: map[string]bool{"ss": true}, reply: map[string]Result{"ss -ltnpH": {Stdout: ssOut}}}
	// pid 77 runs in w1's own worktree; pid 78 sits in w2's block but lives in w1's worktree.
	in := portsInput(f, map[int]string{77: "/p/.worktrees/w1/web", 78: "/p/.worktrees/w1"})
	c := statusOf(Run(context.Background(), in), "ports")
	if c.Status != Warn || !strings.Contains(c.Detail, "20150") || !strings.Contains(c.Detail, "w2") || strings.Contains(c.Detail, "20042") {
		t.Errorf("ports = %+v, want one warn naming 20150 in w2 only", c)
	}
}

func TestPortsQuietWhenListenersBelongToTheirSlot(t *testing.T) {
	f := &fake{have: map[string]bool{"ss": true}, reply: map[string]Result{"ss -ltnpH": {Stdout: ssOut}}}
	in := portsInput(f, map[int]string{77: "/p/.worktrees/w1", 78: "/p/.worktrees/w2/x"})
	if c := statusOf(Run(context.Background(), in), "ports"); c.Name != "" {
		t.Errorf("ports line = %+v, want none", c)
	}
}

func TestPortsFallsBackToLsofAndTreatsUnknownOwnerAsForeign(t *testing.T) {
	f := &fake{have: map[string]bool{"lsof": true}, reply: map[string]Result{"lsof -nP -iTCP -sTCP:LISTEN -Fpn": {Stdout: "p91\nn*:20003\n"}}}
	c := statusOf(Run(context.Background(), portsInput(f, nil)), "ports")
	if c.Status != Warn || !strings.Contains(c.Detail, "20003") {
		t.Errorf("ports = %+v, want a warn naming 20003", c)
	}
}

func TestPortsReportsWhenNoScannerWorks(t *testing.T) {
	for name, f := range map[string]*fake{
		"none installed": {have: map[string]bool{}},
		"ss fails":       {have: map[string]bool{"ss": true}, reply: map[string]Result{"ss -ltnpH": {ExitCode: 1}}},
	} {
		c := statusOf(Run(context.Background(), portsInput(f, nil)), "ports")
		if c.Status != Skip || !strings.Contains(c.Detail, "not checked") {
			t.Errorf("%s: ports = %+v, want a skip saying blocks were not checked", name, c)
		}
	}
}

func TestParseLsofCwdAndDualStackDedupe(t *testing.T) {
	if got := ParseLsofCwd("p91\nfcwd\nn/Users/me/p/.worktrees/w1\n"); got != "/Users/me/p/.worktrees/w1" {
		t.Errorf("ParseLsofCwd = %q", got)
	}
	if got := ParseLsof("p91\nn*:20003\nn[::1]:20003\n"); len(got) != 1 {
		t.Errorf("ParseLsof listed %v, want one entry for the dual-stack port", got)
	}
}

func TestPortsUsesEachSlotsOwnBlockWidth(t *testing.T) {
	f := &fake{have: map[string]bool{"ss": true}, reply: map[string]Result{"ss -ltnpH": {Stdout: ssOut}}}
	in := portsInput(f, map[int]string{77: "/p/.worktrees/w1", 78: "/p/.worktrees/w1"})
	in.Slots[1].Size = 10 // w2 holds 20100-20109: 20150 is outside it
	if c := statusOf(Run(context.Background(), in), "ports"); c.Name != "" {
		t.Errorf("ports line = %+v, want none", c)
	}
}

// codexIn is the doctor input of a project that configures Codex.
func codexIn(homes []Home, tiers bool) map[string]KindInput {
	return map[string]KindInput{harness.Codex: {AccountsKey: "round.accounts", Homes: homes, Tiers: tiers}}
}

// A hook command with a slash resolves through Input.Look alone, after ~ is
// expanded; no file exists on disk.
func TestHookCommandWithSlashResolvesThroughLook(t *testing.T) {
	o := newOrch(t)
	home := t.TempDir()
	o.write(t, filepath.Join(o.root, ".claude", "settings.json"), `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"~/bin/rota hook guard # rota-hook"}]}]}}`)
	run := func(have map[string]bool) Check {
		f := &fake{have: have, reply: map[string]Result{}}
		look := func(name string) (string, bool) { p, _ := f.look(name); return p, have[name] }
		r := Run(context.Background(), Input{Exec: f.exec, Look: look, Home: home, ProjectRoot: o.root, ConfigDirs: []string{o.a, o.b}})
		return statusOf(r, "guard-hook")
	}
	if c := run(map[string]bool{filepath.Join(home, "bin/rota"): true}); c.Status != Pass {
		t.Fatalf("found: %+v", c)
	}
	if c := run(nil); c.Status != Fail || !strings.Contains(c.Detail, "~/bin/rota, which is not found") {
		t.Fatalf("missing: %+v", c)
	}
}

func TestConfigDirCheck(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     Input
		status string
		detail string
	}{
		{"override", Input{ConfigDir: "/p/rota", ConfigDirSource: "override"}, Pass, "/p/rota (ROTA_CONFIG_DIR override)"},
		{"xdg", Input{ConfigDir: "/x/rota", ConfigDirSource: "xdg"}, Pass, "from XDG_CONFIG_HOME"},
		{"default", Input{ConfigDir: "/h/.config/rota", ConfigDirSource: "default"}, Pass, "default ~/.config/rota"},
		{"relative override warns", Input{ConfigDir: "/x/rota", ConfigDirSource: "xdg", ConfigDirWarning: "ROTA_CONFIG_DIR=r is not an absolute path, ignored"}, Warn, "not an absolute path"},
		{"unresolved adds no line", Input{}, "", ""},
	} {
		in := tc.in
		f := &fake{have: map[string]bool{}, reply: map[string]Result{}}
		in.Exec, in.Look = f.exec, f.look
		var got *Check
		rep := Run(context.Background(), in)
		for i := range rep.Checks {
			if rep.Checks[i].Name == "config dir" {
				got = &rep.Checks[i]
			}
		}
		if tc.status == "" {
			if got != nil {
				t.Errorf("%s: unexpected line %+v", tc.name, got)
			}
			continue
		}
		if got == nil || got.Status != tc.status || !strings.Contains(got.Detail, tc.detail) {
			t.Errorf("%s: got %+v", tc.name, got)
		}
	}
}

func TestAccountsKeychainThroughInjectedRunner(t *testing.T) {
	dir := t.TempDir()
	var asked []string
	in := Input{Home: t.TempDir(), Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: dir}},
		Credentials: worker.Credentials{GOOS: "darwin", Run: func(_ context.Context, c proc.Cmd) (proc.Result, error) {
			asked = append(asked, c.Name)
			return proc.Result{Stdout: `{"claudeAiOauth":{"accessToken":"kc"}}`}, nil
		}}}
	r := &runner{in: in, ctx: context.Background()}
	if c := r.accounts(); c.Status != Pass || len(asked) != 1 || asked[0] != "security" {
		t.Errorf("accounts = %+v, runner calls %v, want a pass through security", c, asked)
	}
	in.Credentials.Run = func(context.Context, proc.Cmd) (proc.Result, error) { return proc.Result{ExitCode: 44}, nil }
	r = &runner{in: in, ctx: context.Background()}
	if c := r.accounts(); c.Status != Fail || !strings.Contains(c.Detail, "Keychain") {
		t.Errorf("accounts = %+v, want a fail naming the Keychain", c)
	}
}

func TestStaleMilestoneFilesWarn(t *testing.T) {
	f := &fake{}
	find := func(in Input) Check {
		in.Exec, in.Look = f.exec, f.look
		for _, c := range Run(context.Background(), in).Checks {
			if c.Name == "milestones" {
				return c
			}
		}
		return Check{}
	}
	if c := find(Input{}); c.Name != "" {
		t.Fatalf("no stale files adds no line: %+v", c)
	}
	c := find(Input{StaleMilestoneFiles: []string{"M01.md", "M07.md"}})
	if c.Status != Warn || !strings.Contains(c.Detail, "M01.md, M07.md") || !strings.Contains(c.Hint, "rota migrate milestone-files --apply") {
		t.Fatalf("%+v", c)
	}
}

// A configDir that is neither absolute nor ~-prefixed fails, naming the rule
// (#1777), instead of being looked up under the current directory.
func TestAccountsFailsOnARelativeConfigDir(t *testing.T) {
	f := &fake{}
	in := Input{Home: t.TempDir(), Accounts: []config.Account{{Kind: config.KindClaude, Name: "a", Dir: ".claude-a"}}, Exec: f.exec, Look: f.look}
	c := statusOf(Run(context.Background(), in), "accounts")
	if c.Status != Fail || !strings.Contains(c.Detail, "configDir must be absolute or start with ~") || !strings.Contains(c.Detail, "a:") {
		t.Fatalf("%+v", c)
	}
}
