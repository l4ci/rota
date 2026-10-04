package issues

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// forge is a scripted gh and glab: calls records every argument list; reply
// answers by the joined arguments (first prefix that matches), and git remote
// answers with origin.
type forge struct {
	origin string
	calls  []string
	reply  map[string]reply
}

type reply struct {
	out, err string
	code     int
}

func (f *forge) env(t *testing.T) Env {
	exe := func(_ context.Context, _ string, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		line := strings.Join(args, " ")
		if name == "git" {
			if f.origin == "" {
				return nil, []byte("fatal: no origin"), 2, nil
			}
			return []byte(f.origin + "\n"), nil, 0, nil
		}
		f.calls = append(f.calls, name+" "+line)
		for prefix, r := range f.reply {
			if strings.HasPrefix(line, prefix) {
				return []byte(r.out), []byte(r.err), r.code, nil
			}
		}
		return nil, nil, 0, nil
	}
	look := func(n string) (string, error) { return "/fake/" + n, nil }
	return Env{Settings: tracker.SettingsFromConfig(nil), Opts: []tracker.Option{tracker.WithExec(exe, look)}}
}

func (f *forge) did(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

const gh, gl = "https://github.com/o/r.git", "https://gitlab.com/o/r.git"

func TestProvider(t *testing.T) {
	for url, want := range map[string]string{gh: "github", gl: "gitlab", "": "unknown", "https://example.org/x.git": "unknown",
		"git@gitlab.corp:o/r.git": "gitlab", "ssh://git@GitHub.com/o/r.git": "github"} {
		f := &forge{origin: url}
		if got := Provider(context.Background(), f.env(t), ""); got != want {
			t.Errorf("origin %q: %s, want %s", url, got, want)
		}
	}
}

func TestLabelGitHub(t *testing.T) {
	ctx := context.Background()
	view := reply{out: `{"labels": [{"name": "have"}]}`}
	cases := []struct {
		name        string
		add         bool
		label       string
		auto        bool
		replies     map[string]reply
		wantChanged bool
		wantErr     bool
		wantCalls   []string
	}{
		{"add present", true, "have", true, map[string]reply{"issue view": view}, false, false, []string{"gh issue edit 3 --add-label have"}},
		{"add new", true, "new", true, map[string]reply{"issue view": view}, true, false, []string{"gh issue edit 3 --add-label new"}},
		{"remove present", false, "have", true, map[string]reply{"issue view": view}, true, false, []string{"gh issue edit 3 --remove-label have"}},
		{"remove absent", false, "gone", true, map[string]reply{"issue view": view}, false, false, nil},
		{"unreadable issue", true, "x", true, map[string]reply{"issue view": {code: 1, err: "nope"}}, true, false, nil},
		{"create the label", true, "new", true, map[string]reply{"issue view": view, "issue edit": {code: 1, err: "could not add label: 'new' not found"}}, true, true, []string{"gh label create new --force"}},
		{"no creation allowed", true, "new", false, map[string]reply{"issue view": view, "issue edit": {code: 1, err: "could not add label: 'new' not found"}}, false, true, nil},
		{"other failure", true, "new", true, map[string]reply{"issue view": view, "issue edit": {code: 1, err: "server exploded"}}, false, true, nil},
	}
	for _, c := range cases {
		f := &forge{origin: gh, reply: c.replies}
		changed, err := Label(ctx, f.env(t), "", 3, c.label, c.add, c.auto)
		if changed != c.wantChanged && err == nil || (err != nil) != c.wantErr {
			t.Errorf("%s: changed=%v err=%v", c.name, changed, err)
		}
		for _, w := range c.wantCalls {
			if !f.did(w) {
				t.Errorf("%s: missing call %q in %q", c.name, w, f.calls)
			}
		}
		if c.name == "no creation allowed" && f.did("gh label create") {
			t.Errorf("%s: created a label", c.name)
		}
	}
}

func TestLabelGitLab(t *testing.T) {
	ctx := context.Background()
	f := &forge{origin: gl, reply: map[string]reply{"issue view": {out: `{"labels": ["have"]}`}, "label create": {code: 1, err: "exists"}}}
	changed, err := Label(ctx, f.env(t), "", 3, "have", true, true)
	if err != nil || changed {
		t.Errorf("add present: %v %v", changed, err)
	}
	if !f.did("glab label create --name have") || !f.did("glab issue update 3 --label have") {
		t.Errorf("calls %q", f.calls)
	}
	f = &forge{origin: gl, reply: map[string]reply{"issue view": {out: `{"labels": ["have"]}`}}}
	if changed, err := Label(ctx, f.env(t), "", 3, "have", false, true); err != nil || !changed || !f.did("glab issue update 3 --unlabel have") {
		t.Errorf("remove: %v %v %q", changed, err, f.calls)
	}
	f = &forge{origin: gl}
	if _, err := Label(ctx, f.env(t), "", 3, "x", true, false); err != nil || f.did("glab label create") {
		t.Errorf("no creation: %v %q", err, f.calls)
	}
	// unknown provider
	f = &forge{}
	if _, err := Label(ctx, f.env(t), "", 3, "x", true, true); !errors.Is(err, ErrNoProvider) {
		t.Errorf("unknown provider: %v", err)
	}
	// a missing issue is not found, any other forge failure is not
	f = &forge{origin: gh, reply: map[string]reply{"issue edit": {code: 1, err: "GraphQL: Could not resolve to an Issue with the number of 99."}}}
	if _, err := Label(ctx, f.env(t), "", 99, "x", false, false); !tracker.IsKind(err, tracker.KindNotFound) {
		t.Errorf("missing issue: %v", err)
	}
	f = &forge{origin: gh, reply: map[string]reply{"issue edit": {code: 1, err: "HTTP 500: server error"}}}
	if _, err := Label(ctx, f.env(t), "", 99, "x", false, false); !tracker.IsKind(err, tracker.KindFailed) {
		t.Errorf("forge error: %v", err)
	}
}

func TestProviderFallsBackToConfig(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct{ origin, cfg, want string }{
		{gh, "gitlab", "github"}, // origin wins
		{"", "gitlab", "gitlab"}, // no origin: issues.provider
		{"https://example.org/r.git", "github", "github"},
		{"", "auto", "unknown"},
		{"", "bogus", "unknown"},
	} {
		f := &forge{origin: c.origin}
		env := f.env(t)
		env.Settings.Provider = c.cfg
		if got := Provider(ctx, env, ""); got != c.want {
			t.Errorf("origin %q config %q: %s, want %s", c.origin, c.cfg, got, c.want)
		}
	}
}

// gitProject is a repo with one commit, for the commit checks of Close.
func gitProject(t *testing.T) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "T")
	run("config", "user.email", "t@example.com")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a")
	run("commit", "-q", "-m", "init")
	return dir, run("rev-parse", "--short", "HEAD")
}

func TestCloseGitHub(t *testing.T) {
	ctx := context.Background()
	dir, sha := gitProject(t)
	f := &forge{origin: gh, reply: map[string]reply{"issue view": {out: "OPEN\n"}}}
	changed, err := Close(ctx, f.env(t), dir, 12, "HEAD", "B07")
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	if want := "gh issue close 12 --comment Closed by rota: shipped in " + sha + " ([B07])\n\n<!-- rota:shipped -->"; !f.did(want) {
		t.Errorf("calls %q, want %q", f.calls, want)
	}
	// already closed: no write
	f = &forge{origin: gh, reply: map[string]reply{"issue view": {out: "CLOSED\n"}}}
	if changed, err := Close(ctx, f.env(t), dir, 12, sha, ""); err != nil || changed || f.did("gh issue close") {
		t.Errorf("already closed: %v %v %q", changed, err, f.calls)
	}
	// a failed state read still closes; no item leaves no suffix
	f = &forge{origin: gh, reply: map[string]reply{"issue view": {code: 1, err: "x"}}}
	if changed, err := Close(ctx, f.env(t), dir, 12, sha, ""); err != nil || !changed || !f.did("gh issue close 12 --comment Closed by rota: shipped in "+sha) || f.did("gh issue close 12 --comment Closed by rota: shipped in "+sha+" (") {
		t.Errorf("unreadable state: %v %v %q", changed, err, f.calls)
	}
	// a failing close
	f = &forge{origin: gh, reply: map[string]reply{"issue close": {code: 1, err: "denied"}}}
	var te *tracker.Error
	if _, err := Close(ctx, f.env(t), dir, 12, sha, ""); !errors.As(err, &te) || te.Message != "denied" {
		t.Errorf("failing close: %v", err)
	}
}

func TestCloseGitLab(t *testing.T) {
	ctx := context.Background()
	dir, sha := gitProject(t)
	f := &forge{origin: gl, reply: map[string]reply{"issue view": {out: `{"state": "opened"}`}}}
	if changed, err := Close(ctx, f.env(t), dir, 3, sha, "F2"); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	note, closeCall := -1, -1
	for i, c := range f.calls {
		if c == "glab issue note 3 --message Closed by rota: shipped in "+sha+" ([F2])\n\n<!-- rota:shipped -->" {
			note = i
		}
		if c == "glab issue close 3" {
			closeCall = i
		}
	}
	if note < 0 || closeCall < note {
		t.Errorf("comment must precede the close: %q", f.calls)
	}
	f = &forge{origin: gl, reply: map[string]reply{"issue view": {out: `{"state": "closed"}`}}}
	if changed, err := Close(ctx, f.env(t), dir, 3, sha, ""); err != nil || changed || f.did("glab issue close") || f.did("glab issue note") {
		t.Errorf("already closed: %v %v %q", changed, err, f.calls)
	}
}

func TestCloseRefusals(t *testing.T) {
	ctx := context.Background()
	dir, sha := gitProject(t)
	f := &forge{origin: gh}
	for _, c := range []string{"deadbeef", "-x", "--output=y"} {
		if _, err := Close(ctx, f.env(t), dir, 1, c, ""); !errors.Is(err, ErrCommitNotFound) {
			t.Errorf("commit %q: %v", c, err)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("a forge call before the commit check: %q", f.calls)
	}
	var te *tracker.Error
	if _, err := Close(ctx, (&forge{}).env(t), dir, 1, sha, ""); !errors.Is(err, ErrNoProvider) {
		t.Errorf("unknown provider: %v", err)
	}
	f = &forge{origin: gh, reply: map[string]reply{"issue close": {code: 1, err: "GraphQL: Could not resolve to an Issue with the number of 99."}}}
	if _, err := Close(ctx, f.env(t), dir, 99, sha, ""); !tracker.IsKind(err, tracker.KindNotFound) {
		t.Errorf("missing issue: %v", err)
	}
	f = &forge{origin: gh, reply: map[string]reply{"auth status": {code: 1}}}
	if _, err := Close(ctx, f.env(t), dir, 1, sha, ""); !errors.As(err, &te) || te.Kind != tracker.KindUnavailable {
		t.Errorf("unauthenticated: %v", err)
	}
}

func TestStillOpen(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		provider string
		reply    reply
		want     bool
	}{
		{"github", reply{out: "OPEN\n"}, true},
		{"github", reply{out: "CLOSED\n"}, false},
		{"github", reply{code: 1, err: "gone"}, false},
		{"gitlab", reply{out: `{"state": "opened"}`}, true},
		{"gitlab", reply{out: `{"state": "closed"}`}, false},
		{"gitlab", reply{out: `[`}, false},
		{"gitlab", reply{code: 1}, false},
	}
	for _, c := range cases {
		f := &forge{reply: map[string]reply{"issue view": c.reply}}
		if got := StillOpen(ctx, f.env(t), c.provider, "", 5); got != c.want {
			t.Errorf("%s %+v: %v", c.provider, c.reply, got)
		}
	}
	// a missing CLI drops the entry
	env := Env{Opts: []tracker.Option{tracker.WithExec(nil, func(string) (string, error) { return "", exec.ErrNotFound })}}
	if StillOpen(ctx, env, "github", "", 5) {
		t.Error("kept an entry without a CLI")
	}
}
