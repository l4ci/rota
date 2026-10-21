package issues

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/jsonx"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tracker/jiratest"
	"github.com/l4ci/rota/internal/tracker/lineartest"
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
	return Env{Opts: []tracker.Option{tracker.WithExec(exe, look)}}
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
	known := reply{out: `[{"name": "have"}, {"name": "new"}]`}
	none := reply{out: `[]`}
	// read-back after the write: the issue as the forge holds it then
	withNew := reply{out: `{"labels": [{"name": "have"}, {"name": "new"}, {"name": "x"}]}`}
	without := reply{out: `{"labels": []}`}
	rb := "api repos/{owner}/{repo}/issues/3"
	cases := []struct {
		name        string
		add         bool
		label       string
		auto        bool
		replies     map[string]reply
		wantChanged bool
		wantErr     bool
		wantCalls   []string
		noCalls     []string
	}{
		{"add present", true, "have", true, map[string]reply{"issue view": view, "label list": known, rb: view}, false, false, []string{"gh api -X POST repos/{owner}/{repo}/issues/3/labels"}, nil},
		{"add new", true, "new", true, map[string]reply{"issue view": view, "label list": known, rb: withNew}, true, false, []string{"gh api -X POST repos/{owner}/{repo}/issues/3/labels"}, nil},
		{"remove present", false, "have", true, map[string]reply{"issue view": view, rb: without}, true, false, []string{"gh api -X DELETE repos/{owner}/{repo}/issues/3/labels/have"}, nil},
		{"remove absent", false, "gone", true, map[string]reply{"issue view": view, rb: view}, false, false, nil, nil},
		{"unreadable issue", true, "x", true, map[string]reply{"issue view": {code: 1, err: "nope"}, "label list": reply{out: `[{"name": "x"}]`}, rb: withNew}, true, false, []string{"gh api -X POST repos/{owner}/{repo}/issues/3/labels"}, nil},
		{"create the label", true, "new", true, map[string]reply{"issue view": view, "label list": none, rb: withNew}, true, false, []string{"gh label create new --force", "gh api -X POST repos/{owner}/{repo}/issues/3/labels"}, nil},
		{"no creation allowed", true, "new", false, map[string]reply{"issue view": view, "label list": none}, false, true, nil, []string{"gh label create", "gh api -X POST"}},
		{"other failure", true, "new", true, map[string]reply{"issue view": view, "label list": known, "api -X POST": {code: 1, err: "server exploded"}}, false, true, nil, nil},
	}
	for _, c := range cases {
		f := &forge{origin: gh, reply: c.replies}
		changed, err := Label(ctx, f.env(t), "", tracker.NumKey(3), c.label, c.add, c.auto)
		if changed != c.wantChanged && err == nil || (err != nil) != c.wantErr {
			t.Errorf("%s: changed=%v err=%v", c.name, changed, err)
		}
		for _, w := range c.wantCalls {
			if !f.did(w) {
				t.Errorf("%s: missing call %q in %q", c.name, w, f.calls)
			}
		}
		for _, w := range c.noCalls {
			if f.did(w) {
				t.Errorf("%s: unexpected call %q in %q", c.name, w, f.calls)
			}
		}
	}
}

func TestLabelGitLab(t *testing.T) {
	ctx := context.Background()
	f := &forge{origin: gl, reply: map[string]reply{"issue view": {out: `{"labels": ["have"]}`}}}
	changed, err := Label(ctx, f.env(t), "", tracker.NumKey(3), "have", true, true)
	if err != nil || changed {
		t.Errorf("add present: %v %v", changed, err)
	}
	// glab creates a missing label on the update itself
	if f.did("glab label create") || !f.did("glab issue update 3 --label have") {
		t.Errorf("calls %q", f.calls)
	}
	f = &forge{origin: gl, reply: map[string]reply{"issue view": {out: `{"labels": ["have"]}`}}}
	if changed, err := Label(ctx, f.env(t), "", tracker.NumKey(3), "have", false, true); err != nil || !changed || !f.did("glab issue update 3 --unlabel have") {
		t.Errorf("remove: %v %v %q", changed, err, f.calls)
	}
	f = &forge{origin: gl}
	if _, err := Label(ctx, f.env(t), "", tracker.NumKey(3), "x", true, false); err != nil || f.did("glab label create") {
		t.Errorf("no creation: %v %q", err, f.calls)
	}
	// unknown provider
	f = &forge{}
	if _, err := Label(ctx, f.env(t), "", tracker.NumKey(3), "x", true, true); !errors.Is(err, ErrNoProvider) {
		t.Errorf("unknown provider: %v", err)
	}
	// a missing issue is not found, any other forge failure is not
	f = &forge{origin: gh, reply: map[string]reply{"api -X DELETE": {code: 1, err: "HTTP 404: Not Found"}, "api -X POST": {code: 1, err: "HTTP 404: Not Found"}, "api repos/{owner}/{repo}/issues/99": {code: 1, err: "HTTP 404: Not Found"}}}
	if _, err := Label(ctx, f.env(t), "", tracker.NumKey(99), "x", false, false); !tracker.IsKind(err, tracker.KindNotFound) {
		t.Errorf("missing issue: %v", err)
	}
	f = &forge{origin: gh, reply: map[string]reply{"api -X POST": {code: 1, err: "HTTP 500: server error"}, "api -X DELETE": {code: 1, err: "HTTP 500: server error"}}}
	if _, err := Label(ctx, f.env(t), "", tracker.NumKey(99), "x", false, false); !tracker.IsKind(err, tracker.KindFailed) {
		t.Errorf("forge error: %v", err)
	}
}

func TestProviderFallsBackToConfig(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct{ origin, key, cfg, want string }{
		{gh, "forge", "gitlab", "github"}, // origin wins over forge.provider
		{"", "forge", "gitlab", "gitlab"}, // no origin: forge.provider
		{"https://example.org/r.git", "forge", "github", "github"},
		{"", "forge", "auto", "unknown"},
		{"", "forge", "local", "unknown"},
		{"", "forge", "bogus", "unknown"},
		// issues.source names the tracker outright, whatever the origin says
		{gh, "issues", "gitlab", "gitlab"},
		{"", "issues", "github", "github"},
		{gh, "issues", "file", "github"}, // a file source reads the origin
		{"", "issues", "jira", "unknown"},
	} {
		f := &forge{origin: c.origin}
		env := f.env(t)
		obj := jsonx.NewObject()
		if c.key == "forge" {
			obj.Set("provider", c.cfg)
		} else {
			obj.Set("source", c.cfg)
		}
		root := jsonx.NewObject()
		root.Set(c.key, obj)
		env.Config = root
		if got := Provider(ctx, env, ""); got != c.want {
			t.Errorf("origin %q %s %q: %s, want %s", c.origin, c.key, c.cfg, got, c.want)
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
	f := &forge{origin: gh, reply: map[string]reply{"issue view": {out: `{"state": "OPEN"}`}}}
	changed, err := Close(ctx, f.env(t), dir, tracker.NumKey(12), "HEAD", "B07")
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	if want := "gh issue close 12 --reason completed --comment Closed by rota: shipped in " + sha + " ([B07])\n\n<!-- rota:shipped -->"; !f.did(want) {
		t.Errorf("calls %q, want %q", f.calls, want)
	}
	// already closed: no write
	f = &forge{origin: gh, reply: map[string]reply{"issue view": {out: `{"state": "CLOSED"}`}}}
	if changed, err := Close(ctx, f.env(t), dir, tracker.NumKey(12), sha, ""); err != nil || changed || f.did("gh issue close") {
		t.Errorf("already closed: %v %v %q", changed, err, f.calls)
	}
	// a failed state read still closes; no item leaves no suffix
	f = &forge{origin: gh, reply: map[string]reply{"issue view": {code: 1, err: "x"}}}
	if changed, err := Close(ctx, f.env(t), dir, tracker.NumKey(12), sha, ""); err != nil || !changed || !f.did("gh issue close 12 --reason completed --comment Closed by rota: shipped in "+sha) || f.did("gh issue close 12 --reason completed --comment Closed by rota: shipped in "+sha+" (") {
		t.Errorf("unreadable state: %v %v %q", changed, err, f.calls)
	}
	// a failing close
	f = &forge{origin: gh, reply: map[string]reply{"issue close": {code: 1, err: "denied"}}}
	var te *tracker.Error
	if _, err := Close(ctx, f.env(t), dir, tracker.NumKey(12), sha, ""); !errors.As(err, &te) || te.Message != "denied" {
		t.Errorf("failing close: %v", err)
	}
}

func TestCloseGitLab(t *testing.T) {
	ctx := context.Background()
	dir, sha := gitProject(t)
	f := &forge{origin: gl, reply: map[string]reply{"issue view": {out: `{"state": "opened"}`}}}
	if changed, err := Close(ctx, f.env(t), dir, tracker.NumKey(3), sha, "F2"); err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	note, closeCall := -1, -1
	for i, c := range f.calls {
		if c == "glab issue note 3 -m Closed by rota: shipped in "+sha+" ([F2])\n\n<!-- rota:shipped -->" {
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
	if changed, err := Close(ctx, f.env(t), dir, tracker.NumKey(3), sha, ""); err != nil || changed || f.did("glab issue close") || f.did("glab issue note") {
		t.Errorf("already closed: %v %v %q", changed, err, f.calls)
	}
}

func TestCloseRefusals(t *testing.T) {
	ctx := context.Background()
	dir, sha := gitProject(t)
	f := &forge{origin: gh}
	for _, c := range []string{"deadbeef", "-x", "--output=y"} {
		if _, err := Close(ctx, f.env(t), dir, tracker.NumKey(1), c, ""); !errors.Is(err, ErrCommitNotFound) {
			t.Errorf("commit %q: %v", c, err)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("a forge call before the commit check: %q", f.calls)
	}
	var te *tracker.Error
	if _, err := Close(ctx, (&forge{}).env(t), dir, tracker.NumKey(1), sha, ""); !errors.Is(err, ErrNoProvider) {
		t.Errorf("unknown provider: %v", err)
	}
	f = &forge{origin: gh, reply: map[string]reply{"issue close": {code: 1, err: "GraphQL: Could not resolve to an Issue with the number of 99."}}}
	if _, err := Close(ctx, f.env(t), dir, tracker.NumKey(99), sha, ""); !tracker.IsKind(err, tracker.KindNotFound) {
		t.Errorf("missing issue: %v", err)
	}
	f = &forge{origin: gh, reply: map[string]reply{"auth status": {code: 1}}}
	if _, err := Close(ctx, f.env(t), dir, tracker.NumKey(1), sha, ""); !errors.As(err, &te) || te.Kind != tracker.KindUnavailable {
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
		{"github", reply{out: `{"state": "OPEN"}`}, true},
		{"github", reply{out: `{"state": "CLOSED"}`}, false},
		{"github", reply{code: 1, err: "gone"}, false},
		{"gitlab", reply{out: `{"state": "opened"}`}, true},
		{"gitlab", reply{out: `{"state": "closed"}`}, false},
		{"gitlab", reply{out: `[`}, false},
		{"gitlab", reply{code: 1}, false},
	}
	for _, c := range cases {
		f := &forge{reply: map[string]reply{"issue view": c.reply}}
		if got := StillOpen(ctx, f.env(t), c.provider, "", tracker.NumKey(5)); got != c.want {
			t.Errorf("%s %+v: %v", c.provider, c.reply, got)
		}
	}
	// a missing CLI drops the entry
	env := Env{Opts: []tracker.Option{tracker.WithExec(nil, func(string) (string, error) { return "", exec.ErrNotFound })}}
	if StillOpen(ctx, env, "github", "", tracker.NumKey(5)) {
		t.Error("kept an entry without a CLI")
	}
}

// keyForge is a Jira-shaped tracker: the issue is PROJ-123, a number is not an
// issue there.
type keyForge struct{ *trackertest.Fake }

func (keyForge) CheckAuth(context.Context) error { return nil }

func keyEnv(t *testing.T) (Env, *keyForge) {
	t.Helper()
	k := &keyForge{&trackertest.Fake{Issues: []tracker.Issue{{Key: "PROJ-123", Title: "Widen", State: "open"}}}}
	env := (&forge{origin: gh}).env(t)
	env.Forge = func(context.Context, string, string) (Forge, error) { return k, nil }
	return env, k
}

// AC-3: label, close and the still-open check take a key.
func TestKeyIssueLabelCloseStillOpen(t *testing.T) {
	ctx := context.Background()
	env, k := keyEnv(t)
	key := tracker.IssueKey("PROJ-123")
	if changed, err := Label(ctx, env, "", key, "ready", true, false); err != nil || !changed {
		t.Fatalf("add: %v %v", changed, err)
	}
	if changed, err := Label(ctx, env, "", key, "ready", true, false); err != nil || changed {
		t.Errorf("a label already there is a no-op: %v %v", changed, err)
	}
	if changed, err := Label(ctx, env, "", key, "ready", false, false); err != nil || !changed {
		t.Errorf("remove: %v %v", changed, err)
	}
	if !StillOpen(ctx, env, "github", "", key) {
		t.Error("PROJ-123 is open")
	}
	dir, sha := gitProject(t)
	if changed, err := Close(ctx, env, dir, key, sha, "PROJ-123"); err != nil || !changed {
		t.Fatalf("close: %v %v", changed, err)
	}
	if k.Issues[0].State != "closed" {
		t.Errorf("state %q", k.Issues[0].State)
	}
	if StillOpen(ctx, env, "github", "", key) {
		t.Error("PROJ-123 is closed")
	}
	if changed, err := Close(ctx, env, dir, key, sha, ""); err != nil || changed {
		t.Errorf("closing twice: %v %v", changed, err)
	}
	if _, err := Label(ctx, env, "", "PROJ-999", "x", true, false); !tracker.IsKind(err, tracker.KindNotFound) {
		t.Errorf("unknown key: %v", err)
	}
}

// With issues.source jira, label and close act on the Jira issue through
// issues.connection, whatever the origin remote is.
func TestLabelCloseOnJiraSource(t *testing.T) {
	ctx := context.Background()
	s := jiratest.New(t, jiratest.Options{})
	conn := tracker.WithConnections(func(_ context.Context, name string) (connections.Connection, string, error) {
		return connections.Connection{Name: name, Kind: connections.KindJira, Site: s.URL, User: "me@example.com", Deployment: connections.DeploymentCloud}, "tok", nil
	})
	f := &forge{origin: gh}
	env := f.env(t)
	cfg, err := jsonx.Decode([]byte(`{"issues": {"source": "jira", "connection": "work", "jira": {"project": "PROJ"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	env.Config = cfg
	env.Opts = append(env.Opts, conn)
	it, err := tracker.NewIssueTrackerFromConfig(ctx, env.Config, "", conn)
	if err != nil {
		t.Fatal(err)
	}
	key, err := it.Create(ctx, "Widen", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := Label(ctx, env, "", key, "ready", true, false); err != nil || !changed {
		t.Fatalf("label: %v %v", changed, err)
	}
	if is, _ := it.Get(ctx, key, false); !slices.Contains(is.Labels, "ready") {
		t.Fatalf("labels %v", is.Labels)
	}
	dir, sha := gitProject(t)
	if changed, err := Close(ctx, env, dir, key, sha, ""); err != nil || !changed {
		t.Fatalf("close: %v %v", changed, err)
	}
	if st := s.StatusOf(string(key)); st != "Done" {
		t.Errorf("status %q", st)
	}
	if len(f.calls) != 0 {
		t.Errorf("a forge CLI ran: %q", f.calls)
	}
}

// With issues.source linear, label and close act on the Linear issue through
// issues.connection, whatever the origin remote is.
func TestLabelCloseOnLinearSource(t *testing.T) {
	ctx := context.Background()
	s := lineartest.New(t, lineartest.Options{})
	s.AddLabel("ready", "")
	conn := tracker.WithConnections(func(_ context.Context, name string) (connections.Connection, string, error) {
		return connections.Connection{Name: name, Kind: connections.KindLinear, Site: s.URL}, "lin_api_test", nil
	})
	f := &forge{origin: gh}
	env := f.env(t)
	cfg, err := jsonx.Decode([]byte(`{"issues": {"source": "linear", "connection": "work", "linear": {"team": "ENG"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	env.Config = cfg
	env.Opts = append(env.Opts, conn)
	it, err := tracker.NewIssueTrackerFromConfig(ctx, env.Config, "", conn)
	if err != nil {
		t.Fatal(err)
	}
	key, err := it.Create(ctx, "Widen", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := Label(ctx, env, "", key, "ready", true, false); err != nil || !changed {
		t.Fatalf("label: %v %v", changed, err)
	}
	if got := s.Labels(string(key)); !slices.Contains(got, "ready") {
		t.Fatalf("labels %v", got)
	}
	dir, sha := gitProject(t)
	if changed, err := Close(ctx, env, dir, key, sha, ""); err != nil || !changed {
		t.Fatalf("close: %v %v", changed, err)
	}
	if st := s.StateType(string(key)); st != "completed" {
		t.Errorf("state type %q", st)
	}
	if len(f.calls) != 0 {
		t.Errorf("a forge CLI ran: %q", f.calls)
	}
}
