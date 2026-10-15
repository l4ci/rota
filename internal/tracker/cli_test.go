package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
)

// fakeCLI scripts forge CLI answers and logs every call, like the fake gh in
// smoke section 54.
type fakeCLI struct {
	calls  [][]string
	dirs   []string
	stdins [][]byte
	answer func(n int, args []string) (string, string, int)
	slept  []time.Duration
}

func (f *fakeCLI) cli(provider string) *CLI {
	return &CLI{
		Provider:  provider,
		RetryWait: 7 * time.Second,
		Exec: func(_ context.Context, dir, name string, args []string, stdin []byte) ([]byte, []byte, int, error) {
			f.calls = append(f.calls, append([]string{name}, args...))
			f.dirs = append(f.dirs, dir)
			f.stdins = append(f.stdins, stdin)
			out, errs, code := "[]", "", 0
			if f.answer != nil {
				out, errs, code = f.answer(len(f.calls), args)
			}
			return []byte(out), []byte(errs), code, nil
		},
		LookPath: func(string) (string, error) { return "/fake", nil },
		Sleep:    func(d time.Duration) { f.slept = append(f.slept, d) },
	}
}

func (f *fakeCLI) last() string { return strings.Join(f.calls[len(f.calls)-1], " ") }

func TestRunInjectsLimitsAndPagination(t *testing.T) {
	cases := []struct {
		provider string
		args     string
		want     string
	}{
		{"github", "issue list", "gh issue list --limit 1000"},
		{"github", "pr ls", "gh pr ls --limit 1000"},
		{"gitlab", "issue list", "glab issue list --per-page 100"},
		{"gitlab", "mr list", "glab mr list --per-page 100"},
		{"github", "issue list -L 5", "gh issue list -L 5"},
		{"github", "issue list -L5", "gh issue list -L5"},
		{"github", "issue list --limit=5", "gh issue list --limit=5"},
		{"gitlab", "mr list --per-page 20", "glab mr list --per-page 20"},
		{"gitlab", "mr list -P20", "glab mr list -P20"},
		{"gitlab", "pr list", "glab pr list"},
		{"github", "mr list", "gh mr list"},
		{"github", "issue view 3", "gh issue view 3"},
		{"github", "label list", "gh label list"},
		{"github", "api repos/o/r/issues", "gh api repos/o/r/issues --paginate"},
		{"github", "api -X GET repos/o/r/issues", "gh api -X GET repos/o/r/issues --paginate"},
		{"github", "api -X POST repos/o/r/issues", "gh api -X POST repos/o/r/issues"},
		{"github", "api -XPATCH repos/o/r/issues", "gh api -XPATCH repos/o/r/issues"},
		{"github", "api --method=delete x", "gh api --method=delete x"},
		{"github", "api -X POST -X GET x", "gh api -X POST -X GET x --paginate"},
		{"github", "api repos/o/r/issues -f title=x", "gh api repos/o/r/issues -f title=x"},
		{"github", "api x -ftitle=x", "gh api x -ftitle=x"},
		{"github", "api x --raw-field=a=b", "gh api x --raw-field=a=b"},
		{"github", "api x --input body.json", "gh api x --input body.json"},
		{"github", "api x --paginate", "gh api x --paginate"},
		{"gitlab", "api projects/:id/issues", "glab api projects/:id/issues --paginate"},
	}
	for _, c := range cases {
		f := &fakeCLI{}
		if _, err := f.cli(c.provider).Run(context.Background(), strings.Fields(c.args), nil); err != nil {
			t.Fatalf("%s: %v", c.args, err)
		}
		if got := f.last(); got != c.want {
			t.Errorf("%s %q: ran %q, want %q", c.provider, c.args, got, c.want)
		}
	}
}

func TestRunStdinOnlyWhenAnArgumentTakesIt(t *testing.T) {
	for _, c := range []struct {
		args string
		want []byte
	}{
		{"issue create -F -", []byte("body")},
		{"api x -F body=@-", []byte("body")},
		{"api x -f body=-", []byte("body")},
		{"issue view 3", nil},
	} {
		f := &fakeCLI{}
		if _, err := f.cli("github").Run(context.Background(), strings.Fields(c.args), strings.NewReader("body")); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(f.stdins[0], c.want) {
			t.Errorf("%q: stdin %q, want %q", c.args, f.stdins[0], c.want)
		}
	}
	// An argument that takes stdin with no reader gets an empty one, not none.
	f := &fakeCLI{}
	if _, err := f.cli("github").Run(context.Background(), []string{"issue", "create", "-F", "-"}, nil); err != nil {
		t.Fatal(err)
	}
	if f.stdins[0] == nil || len(f.stdins[0]) != 0 {
		t.Errorf("stdin %#v, want empty", f.stdins[0])
	}
}

func TestRunStdinReplayedOnRetry(t *testing.T) {
	f := &fakeCLI{answer: func(n int, _ []string) (string, string, int) {
		if n == 1 {
			return "", "HTTP 429: too many", 1
		}
		return "{}", "", 0
	}}
	if _, err := f.cli("github").Run(context.Background(), []string{"issue", "create", "-F", "-"}, strings.NewReader("body")); err != nil {
		t.Fatal(err)
	}
	if len(f.stdins) != 2 || string(f.stdins[1]) != "body" {
		t.Fatalf("stdins %q, want body twice", f.stdins)
	}
}

func TestRunRunsInDir(t *testing.T) {
	f := &fakeCLI{}
	c := f.cli("github")
	c.Dir = "/some/sub"
	if _, err := c.Run(context.Background(), []string{"issue", "view", "1"}, nil); err != nil {
		t.Fatal(err)
	}
	if f.dirs[0] != "/some/sub" {
		t.Fatalf("dir %q", f.dirs[0])
	}
}

func TestRunTruncationWarning(t *testing.T) {
	rows := func(n int) string {
		b, _ := json.Marshal(make([]struct{}, n))
		return string(b)
	}
	for _, c := range []struct {
		provider string
		args     string
		n        int
		warn     bool
	}{
		{"github", "issue list", 1000, true},
		{"github", "issue list", 3, false},
		{"github", "issue list -L 1000", 1000, false},
		{"gitlab", "mr list", 100, true},
	} {
		f := &fakeCLI{answer: func(int, []string) (string, string, int) { return rows(c.n), "note\n", 0 }}
		r, err := f.cli(c.provider).Run(context.Background(), strings.Fields(c.args), nil)
		if err != nil {
			t.Fatal(err)
		}
		warned := strings.Contains(string(r.Stderr), "hit the list limit")
		if warned != c.warn || !strings.HasPrefix(string(r.Stderr), "note\n") {
			t.Errorf("%s %q n=%d: stderr %q, want warning %v", c.provider, c.args, c.n, r.Stderr, c.warn)
		}
	}
}

func TestRunRateLimits(t *testing.T) {
	answer := func(stderr string, okOn int) func(int, []string) (string, string, int) {
		return func(n int, _ []string) (string, string, int) {
			if okOn > 0 && n >= okOn {
				return "[]", "", 0
			}
			return "", stderr, 1
		}
	}
	cases := []struct {
		name   string
		answer func(int, []string) (string, string, int)
		calls  int
		kind   Kind
		code   int
		msg    string
		slept  int
	}{
		{"primary once", answer("HTTP 429: too many", 2), 2, -1, 0, "", 1},
		{"primary twice", answer("API rate limit exceeded for user", 0), 2, KindRateLimited, 4, "github rate limit — stopped after one retry", 1},
		{"secondary", answer("You have exceeded a secondary rate limit", 0), 1, KindRateLimited, 4, "github secondary rate limit — stopped; wait several minutes before retrying", 0},
		{"abuse", answer("abuse detection mechanism", 0), 1, KindRateLimited, 4, "", 0},
		{"primary then secondary", func(n int, _ []string) (string, string, int) {
			if n == 1 {
				return "", "rate limit exceeded", 1
			}
			return "", "secondary rate limit", 1
		}, 2, KindRateLimited, 4, "github secondary rate limit — stopped; wait several minutes before retrying", 1},
		{"primary then plain failure", func(n int, _ []string) (string, string, int) {
			if n == 1 {
				return "", "rate limit exceeded", 1
			}
			return "", "boom", 9
		}, 2, -1, 0, "", 1},
		{"auth", answer("To get started, please run: gh auth login", 0), 1, KindUnavailable, 3, "gh is not authenticated; run 'gh auth login'", 0},
		{"401", answer("HTTP 401: Bad credentials", 0), 1, KindUnavailable, 3, "", 0},
	}
	for _, c := range cases {
		f := &fakeCLI{answer: c.answer}
		_, err := f.cli("github").Run(context.Background(), []string{"issue", "list"}, nil)
		if len(f.calls) != c.calls || len(f.slept) != c.slept {
			t.Errorf("%s: %d calls, %d sleeps; want %d, %d", c.name, len(f.calls), len(f.slept), c.calls, c.slept)
		}
		if c.slept > 0 && f.slept[0] != 7*time.Second {
			t.Errorf("%s: slept %v, want RetryWait", c.name, f.slept[0])
		}
		if c.kind < 0 {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		var e *Error
		if !errors.As(err, &e) || e.Kind != c.kind || e.Code != c.code || (c.msg != "" && e.Message != c.msg) {
			t.Errorf("%s: err %#v, want kind %d code %d %q", c.name, err, c.kind, c.code, c.msg)
		}
		if IsCLIMissing(err) {
			t.Errorf("%s: a CLI that ran is not a missing CLI: %v", c.name, err)
		}
	}
}

func TestRunPlainFailurePassesThrough(t *testing.T) {
	f := &fakeCLI{answer: func(int, []string) (string, string, int) { return "partial", "boom: not found", 7 }}
	r, err := f.cli("github").Run(context.Background(), []string{"issue", "view", "9"}, nil)
	if err != nil || r.ExitCode != 7 || string(r.Stdout) != "partial" || string(r.Stderr) != "boom: not found" || len(f.calls) != 1 {
		t.Fatalf("got %+v, %v after %d calls", r, err, len(f.calls))
	}
}

func TestRunMissingCLI(t *testing.T) {
	for p, cli := range map[string]string{"github": "gh", "gitlab": "glab"} {
		f := &fakeCLI{}
		c := f.cli(p)
		c.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
		_, err := c.Run(context.Background(), []string{"issue", "list"}, nil)
		if !IsKind(err, KindUnavailable) || !IsCLIMissing(err) || err.Error() != cli+" is not installed" || len(f.calls) != 0 {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestResolveProvider(t *testing.T) {
	origin := func(url string, code int) Exec {
		return func(_ context.Context, dir, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
			if name != "git" || strings.Join(args, " ") != "remote get-url origin" || dir != "/repo" {
				return nil, nil, 0, fmt.Errorf("unexpected %s %q in %q", name, args, dir)
			}
			return []byte(url + "\n"), nil, code, nil
		}
	}
	gh := origin("git@github.com:o/r.git", 0)
	for _, c := range []struct {
		want, configured string
		x                Exec
		out              string
	}{
		{"gitlab", "github", gh, "gitlab"},
		{"auto", "gitlab", gh, "gitlab"},
		{"", "github", gh, "github"},
		{"", "auto", gh, "github"},
		{"", "bogus", origin("https://gitlab.example.com/o/r", 0), "gitlab"},
		{"", "auto", origin("", 2), "local"}, // no remote: local
		{"", "auto", origin("", 0), "local"},
		{"", "local", gh, "local"},
		{"local", "github", gh, "local"},
		{"auto", "local", gh, "local"},
		{"", "auto", origin("https://example.com/o/r", 0), ""},
	} {
		p, err := ResolveProvider(context.Background(), c.want, c.configured, "/repo", c.x)
		if c.out == "" {
			var te *Error
			if !IsKind(err, KindUnavailable) || !strings.Contains(err.Error(), "cannot determine the forge from origin https://example.com/o/r (set forge.provider)") ||
				!errors.As(err, &te) || te.Code != 3 {
				t.Errorf("%+v: %q, %v", c, p, err)
			}
			continue
		}
		if p != c.out || err != nil {
			t.Errorf("%+v: %q, %v; want %q", c, p, err, c.out)
		}
	}
}

// TestProviderFromURL pins the classification table, which the retired
// issues-provider helper agreed with on every row.
func TestProviderFromURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:o/r.git":                 "github",
		"https://github.com/o/r":                 "github",
		"ssh://git@GitHub.Example.com/o/r":       "github",
		"https://gitlab.com/o/r.git":             "gitlab",
		"git@gitlab.internal:grp/sub/r.git":      "gitlab",
		"https://code.example.com/github/r":      "unknown",
		"https://example.com/o/r":                "unknown",
		"/srv/mirrors/github-r":                  "github",
		"file:///srv/r":                          "unknown",
		"https://user@gitlab.example.com:8443/r": "gitlab",
	}
	for url, want := range cases {
		if got := ProviderFromURL(url); got != want {
			t.Errorf("ProviderFromURL(%q) = %q, want %q", url, got, want)
		}
	}
}

// TestProviderFromOrigin pins the one resolver: origin decides, configured is
// only the fallback, and ProviderUnknown is the single unknown answer.
func TestProviderFromOrigin(t *testing.T) {
	for _, c := range []struct{ origin, configured, want string }{
		{"git@github.com:o/r.git", "", "github"},
		{"https://gitlab.com/o/r", "github", "gitlab"},
		{"https://gitlab.corp.example/o/r", "", "gitlab"},
		{"https://github.example.com/o/r", "", "github"},
		{"https://git.example.com/o/r", "gitlab", "gitlab"},
		{"", "github", "github"},
		{"https://git.example.com/o/r", "bogus", "unknown"},
		{"", "", "unknown"},
	} {
		if got := ProviderFromOrigin(c.origin, c.configured); got != c.want {
			t.Errorf("ProviderFromOrigin(%q, %q) = %q, want %q", c.origin, c.configured, got, c.want)
		}
	}
}

func TestSettingsFromConfig(t *testing.T) {
	parse := func(s string) any {
		v, err := jsonx.Decode([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	def := Settings{Provider: "auto", Source: "file", RetryWait: 60 * time.Second, NotPlannedLabel: "not-planned"}
	for _, c := range []struct {
		cfg  any
		want Settings
	}{
		{nil, def},
		{parse(`{}`), def},
		{parse(`{"forge":{"provider":null},"issues":{"source":null,"retryWaitSeconds":null}}`), def},
		{parse(`{"forge":{"provider":"gitlab"},"issues":{"source":"jira","retryWaitSeconds":0.5,"labels":{"notPlanned":"wontfix"}}}`),
			Settings{Provider: "gitlab", Source: "jira", RetryWait: 500 * time.Millisecond, NotPlannedLabel: "wontfix"}},
		// an unmigrated legacy config keeps deciding
		{parse(`{"backlog":{"backend":"issues"},"issues":{"provider":"gitlab"}}`),
			Settings{Provider: "gitlab", Source: "gitlab", RetryWait: 60 * time.Second, NotPlannedLabel: "not-planned"}},
	} {
		if got := SettingsFromConfig(c.cfg); got != c.want {
			t.Errorf("%v: %+v, want %+v", c.cfg, got, c.want)
		}
	}
}

func TestAdapterParseErrors(t *testing.T) {
	answers := map[string]string{
		"issue view":   "not json",
		"issue create": "created, no url",
		"api":          "[1][",
	}
	f := &fakeCLI{answer: func(_ int, args []string) (string, string, int) {
		return answers[args[0]+" "+args[1]], "", 0
	}}
	f2 := &fakeCLI{answer: func(int, []string) (string, string, int) { return "[{}] {}", "", 0 }}
	gh := &GitHub{base: base{cli: f.cli("github"), closing: closingGH}}
	_, err1 := gh.Get(context.Background(), NumKey(1), false)
	_, err2 := gh.Create(context.Background(), "t", "b", nil, "")
	_, err3 := (&GitHub{base: base{cli: f2.cli("github"), closing: closingGH}}).Comments(context.Background(), NumKey(1))
	for i, err := range []error{err1, err2, err3} {
		var e *Error
		if !errors.As(err, &e) || e.Kind != KindFailed || e.Code != 1 {
			t.Errorf("case %d: %#v", i, err)
		}
	}
}

func TestPagesConcatenates(t *testing.T) {
	f := &fakeCLI{answer: func(int, []string) (string, string, int) {
		return `[{"id":1,"body":"a","user":{"login":"x"}}]` + "\n" + `[{"id":"IC_2","body":"b"}]` + "\n", "", 0
	}}
	cs, err := (&GitHub{base: base{cli: f.cli("github"), closing: closingGH}}).Comments(context.Background(), NumKey(5))
	want := []Comment{{ID: "1", Body: "a", Author: "x"}, {ID: "IC_2", Body: "b"}}
	if err != nil || !reflect.DeepEqual(cs, want) {
		t.Fatalf("%+v, %v", cs, err)
	}
	if f.last() != "gh api repos/{owner}/{repo}/issues/5/comments --paginate" {
		t.Fatalf("ran %q", f.last())
	}
}

func TestNewFromConfigOrGitHubFallsBack(t *testing.T) {
	x := func(_ context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		if name == "git" {
			return []byte("https://example.com/x/y.git\n"), nil, 0, nil
		}
		return []byte("[]"), nil, 0, nil
	}
	opts := []Option{WithExec(x, func(string) (string, error) { return "/fake", nil })}

	if _, err := NewFromConfig(context.Background(), nil, "", "", opts...); err == nil {
		t.Fatal("NewFromConfig: want an error for an unrecognized origin")
	}
	a, err := NewFromConfigOrGitHub(context.Background(), nil, "", "", opts...)
	if err != nil || a.Provider() != "github" {
		t.Fatalf("fallback = %v, %v; want github", a, err)
	}
	cfg, _ := jsonx.Decode([]byte(`{"issues":{"provider":"gitlab"}}`))
	a, err = NewFromConfigOrGitHub(context.Background(), cfg, "", "", opts...)
	if err != nil || a.Provider() != "gitlab" {
		t.Fatalf("configured provider = %v, %v; want gitlab, no fallback", a, err)
	}
}

// The two constructors of the seam: the forge resolves from forge.provider and
// origin (no remote is local), the issue tracker from issues.source.
func TestNewForgeAndNewIssueTracker(t *testing.T) {
	origin := func(url string, code int) Option {
		return WithExec(func(_ context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
			return []byte(url + "\n"), nil, code, nil
		}, func(string) (string, error) { return "/bin/x", nil })
	}
	ctx := context.Background()

	// forge: auto resolves from origin; no remote is local; local is the sentinel.
	if f, err := NewForge(ctx, Settings{Provider: "auto"}, "", "/repo", origin("git@github.com:o/r.git", 0)); err != nil || f.Provider() != "github" {
		t.Errorf("auto+github origin: %v, %v", f, err)
	}
	if _, err := NewForge(ctx, Settings{Provider: "auto"}, "", "/repo", origin("", 2)); !errors.Is(err, ErrLocalForge) {
		t.Errorf("auto, no remote: %v, want ErrLocalForge", err)
	}
	if _, err := NewForge(ctx, Settings{Provider: "local"}, "", "/repo", origin("git@github.com:o/r.git", 0)); !errors.Is(err, ErrLocalForge) {
		t.Errorf("local: %v, want ErrLocalForge", err)
	}
	if f, err := NewForge(ctx, Settings{Provider: "gitlab"}, "", "/repo", origin("", 2)); err != nil || f.Provider() != "gitlab" {
		t.Errorf("explicit gitlab, no remote: %v, %v", f, err)
	}

	// issue tracker: source decides, whatever the origin or forge says.
	if it, err := NewIssueTracker(ctx, Settings{Source: "gitlab", Provider: "github"}, "", "/repo", origin("git@github.com:o/r.git", 0)); err != nil || it.Provider() != "gitlab" {
		t.Errorf("gitlab source: %v, %v", it, err)
	}
	if _, err := NewIssueTracker(ctx, Settings{Source: "file"}, "", "/repo"); !errors.Is(err, ErrNoIssueTracker) {
		t.Errorf("file source: %v", err)
	}
	if _, err := NewIssueTracker(ctx, Settings{Source: "linear"}, "", "/repo"); !IsKind(err, KindUnavailable) || !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("linear source: %v", err)
	}
	if _, err := NewIssueTracker(ctx, Settings{Source: "jira", JiraProject: "PROJ"}, "", "/repo"); !IsKind(err, KindUnavailable) || !strings.Contains(err.Error(), "issues.connection") {
		t.Errorf("jira source, no connection: %v", err)
	}
	jiraConn := WithConnections(func(_ context.Context, name string) (connections.Connection, string, error) {
		return connections.Connection{Name: name, Kind: "jira", Site: "https://acme.example", User: "me@acme.example", Deployment: connections.DeploymentCloud}, "tok", nil
	})
	if it, err := NewIssueTracker(ctx, Settings{Source: "jira", Connection: "work", JiraProject: "PROJ"}, "", "/repo", jiraConn); err != nil || it.Provider() != "jira" {
		t.Errorf("jira source: %v, %v", it, err)
	}
	// a legacy "issues backend" with no named provider resolves from origin.
	if it, err := NewIssueTracker(ctx, Settings{Source: "issues"}, "", "/repo", origin("https://gitlab.com/o/r", 0)); err != nil || it.Provider() != "gitlab" {
		t.Errorf("legacy issues: %v, %v", it, err)
	}
	// issues.source forge: each repo's own forge, resolved from its own origin.
	if it, err := NewIssueTracker(ctx, Settings{Source: "forge", Provider: "auto"}, "", "/repo", origin("https://gitlab.com/o/r", 0)); err != nil || it.Provider() != "gitlab" {
		t.Errorf("forge source, gitlab origin: %v, %v", it, err)
	}
	if it, err := NewIssueTracker(ctx, Settings{Source: "forge", Provider: "auto"}, "", "/repo", origin("git@github.com:o/r.git", 0)); err != nil || it.Provider() != "github" {
		t.Errorf("forge source, github origin: %v, %v", it, err)
	}
	if _, err := NewIssueTracker(ctx, Settings{Source: "forge", Provider: "auto"}, "", "/repo", origin("", 2)); !IsKind(err, KindUnavailable) {
		t.Errorf("forge source, no remote: %v", err)
	}
	// a file backlog with a github forge still has a forge.
	if f, err := NewForge(ctx, Settings{Source: "file", Provider: "github"}, "", "/repo", origin("", 2)); err != nil || f.Provider() != "github" {
		t.Errorf("file source, github forge: %v, %v", f, err)
	}
}

// An invalid forge.provider or issues.source is a corrupt config (exit 70),
// not a silent default.
func TestFromConfigConstructorsRejectAnInvalidSource(t *testing.T) {
	ctx := context.Background()
	for _, raw := range []string{`{"forge":{"provider":"bitbucket"}}`, `{"issues":{"source":"svn"}}`} {
		cfg, _ := jsonx.Decode([]byte(raw))
		if err := ValidateConfig(cfg); !IsKind(err, KindInternal) {
			t.Errorf("%s: ValidateConfig %v", raw, err)
		}
		if _, err := NewForgeFromConfig(ctx, cfg, "", "/repo"); !IsKind(err, KindInternal) {
			t.Errorf("%s: NewForgeFromConfig %v", raw, err)
		}
		if _, err := NewIssueTrackerFromConfig(ctx, cfg, "/repo"); !IsKind(err, KindInternal) {
			t.Errorf("%s: NewIssueTrackerFromConfig %v", raw, err)
		}
		if _, err := NewFromConfigOrGitHub(ctx, cfg, "", "/repo"); !IsKind(err, KindInternal) {
			t.Errorf("%s: NewFromConfigOrGitHub fell back: %v", raw, err)
		}
	}
}
