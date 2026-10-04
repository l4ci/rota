package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/tracker"
)

// forge is a scripted gh/glab for the tracker verbs. Every test also puts a
// tripwire gh/glab first on PATH, so a call that escaped the fake fails.
type forge struct {
	calls  []string
	dirs   []string
	stdins []string
	answer func(name string, args []string) (string, string, int)
	found  bool
}

func useForge(t *testing.T, f *forge) {
	t.Helper()
	trip := t.TempDir()
	for _, cli := range []string{"gh", "glab"} {
		script := "#!/bin/sh\necho 'tripwire: real forge CLI reached from a test' >&2\nexit 99\n"
		if err := os.WriteFile(filepath.Join(trip, cli), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", trip+string(os.PathListSeparator)+os.Getenv("PATH"))
	if p, _ := exec.LookPath("gh"); p != filepath.Join(trip, "gh") {
		t.Fatalf("gh resolves to %q, not the tripwire", p)
	}
	f.found = true
	x := func(_ context.Context, dir, name string, args []string, stdin []byte) ([]byte, []byte, int, error) {
		f.calls = append(f.calls, name+" "+strings.Join(args, " "))
		f.dirs = append(f.dirs, dir)
		f.stdins = append(f.stdins, string(stdin))
		out, errs, code := "", "", 0
		if f.answer != nil {
			out, errs, code = f.answer(name, args)
		}
		return []byte(out), []byte(errs), code, nil
	}
	look := func(name string) (string, error) {
		if !f.found {
			return "", exec.ErrNotFound
		}
		return "/fake/" + name, nil
	}
	trackerOptions = []tracker.Option{tracker.WithExec(x, look), tracker.WithSleep(func(time.Duration) {})}
	t.Cleanup(func() { trackerOptions = nil })
}

// trProject is a project dir with .rota/config.json holding cfg.
func trProject(t *testing.T, cfg string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

type trOut struct {
	code           int
	stdout, stderr string
}

func trRun(t *testing.T, dir, stdin string, args ...string) trOut {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	var so, se bytes.Buffer
	code := Main(args, strings.NewReader(stdin), &so, &se)
	return trOut{code, so.String(), se.String()}
}

func envelope(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, s)
	}
	return v
}

func TestTrackerCallPassthrough(t *testing.T) {
	f := &forge{answer: func(string, []string) (string, string, int) { return "[1]\nno newline", "note\n", 0 }}
	useForge(t, f)
	dir := trProject(t, `{"issues":{"provider":"github"}}`)

	o := trRun(t, dir, "", "tracker", "call", "--", "issue", "view", "3")
	if o.code != 0 || o.stdout != "[1]\nno newline" || o.stderr != "note\n" {
		t.Fatalf("text mode must pass output through unchanged: %+v", o)
	}
	if f.calls[0] != "gh issue view 3" {
		t.Fatalf("ran %q", f.calls[0])
	}

	o = trRun(t, dir, "", "tracker", "call", "--json", "--", "issue", "list")
	env := envelope(t, o.stdout)
	data := env["data"].(map[string]any)
	if o.code != 0 || env["ok"] != true || data["provider"] != "github" || data["exitCode"] != 0.0 ||
		data["stdout"] != "[1]\nno newline" || data["stderr"] != "note\n" || o.stderr != "" {
		t.Fatalf("json: %+v", o)
	}
	if f.calls[1] != "gh issue list --limit 1000" {
		t.Fatalf("list limit not injected: %q", f.calls[1])
	}
}

func TestTrackerCallFailures(t *testing.T) {
	dir := trProject(t, `{"issues":{"provider":"github","retryWaitSeconds":0}}`)
	cases := []struct {
		name   string
		answer func(string, []string) (string, string, int)
		found  bool
		args   []string
		code   int
		has    string
	}{
		{"cli exits non-zero", func(string, []string) (string, string, int) { return "", "boom", 7 }, true,
			[]string{"--", "issue", "view", "9"}, 1, "gh exited 7"},
		{"rate limited", func(string, []string) (string, string, int) { return "", "secondary rate limit", 1 }, true,
			[]string{"--", "issue", "list"}, 6, "secondary rate limit"},
		{"not authenticated", func(string, []string) (string, string, int) { return "", "run gh auth login", 1 }, true,
			[]string{"--", "issue", "list"}, 5, "not authenticated"},
		{"cli missing", nil, false, []string{"--", "issue", "list"}, 5, "gh is not installed"},
		{"no cli args", nil, true, []string{"--"}, 2, "no CLI arguments"},
		{"bad provider", nil, true, []string{"--provider", "bitbucket", "--", "x"}, 2, "--provider"},
	}
	for _, c := range cases {
		f := &forge{answer: c.answer}
		useForge(t, f)
		f.found = c.found
		o := trRun(t, dir, "", append([]string{"tracker", "call"}, c.args...)...)
		if o.code != c.code || !strings.Contains(o.stderr, c.has) {
			t.Errorf("%s: exit %d, stderr %q; want %d with %q", c.name, o.code, o.stderr, c.code, c.has)
		}
	}
	// exit 1 carries the CLI's answer in data.exitCode.
	useForge(t, &forge{answer: func(string, []string) (string, string, int) { return "partial", "boom", 7 }})
	o := trRun(t, dir, "", "tracker", "call", "--json", "--", "issue", "view", "9")
	env := envelope(t, o.stdout)
	data, _ := env["data"].(map[string]any)
	if o.code != 1 || data["exitCode"] != 7.0 || data["stdout"] != "partial" {
		t.Fatalf("exit-1 data: %+v", o)
	}
}

func TestTrackerCallProviderAndRepo(t *testing.T) {
	f := &forge{answer: func(string, []string) (string, string, int) { return "[]", "", 0 }}
	useForge(t, f)
	dir := trProject(t, `{"issues":{"provider":"gitlab"}}`)
	sub := filepath.Join(dir, "svc")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rota", "repos.json"), []byte(`{"repos":[{"name":"svc","path":"svc"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	trRun(t, dir, "", "tracker", "call", "--", "mr", "list")
	trRun(t, dir, "", "tracker", "call", "--provider", "github", "--", "pr", "list")
	trRun(t, dir, "", "tracker", "call", "--repo", "svc", "--", "issue", "view", "1")
	want := []string{"glab mr list --per-page 100", "gh pr list --limit 1000", "glab issue view 1"}
	for i, w := range want {
		if f.calls[i] != w {
			t.Errorf("call %d: %q, want %q", i, f.calls[i], w)
		}
	}
	real, _ := filepath.EvalSymlinks(sub)
	if f.dirs[2] != real {
		t.Errorf("--repo ran in %q, want %q", f.dirs[2], real)
	}
	// stdin reaches the CLI only when an argument takes it.
	trRun(t, dir, "body text", "tracker", "call", "--", "issue", "create", "-F", "-")
	trRun(t, dir, "body text", "tracker", "call", "--", "issue", "view", "1")
	if f.stdins[3] != "body text" || f.stdins[4] != "" {
		t.Errorf("stdins %q", f.stdins[3:])
	}
	if o := trRun(t, dir, "", "tracker", "call", "--repo", "nope", "--", "x"); o.code != 3 {
		t.Errorf("unknown --repo: %+v", o)
	}
}

func TestTrackerSuggestUpstream(t *testing.T) {
	// A confirmed pass is audited under .rota/ (B1), so the project needs one.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	ok := []string{"--confirm", "--confirm-note", "yes, file it"}
	body := filepath.Join(dir, "body.md")
	if err := os.WriteFile(body, []byte("learned this\r\nand that\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := func(name string, args []string) (string, string, int) {
		if args[0] == "auth" {
			return "", "", 0
		}
		return "https://github.com/l4ci/rota/issues/123\n", "", 0
	}

	f := &forge{answer: created}
	useForge(t, f)
	t.Setenv("ROTA_UPSTREAM_REPO", "")
	o := trRun(t, dir, "", append([]string{"tracker", "suggest-upstream", "--json", "--title", "A learning", "--body-file", body}, ok...)...)
	env := envelope(t, o.stdout)
	data := env["data"].(map[string]any)
	if o.code != 0 || data["url"] != "https://github.com/l4ci/rota/issues/123" || data["number"] != 123.0 ||
		data["upstreamRepo"] != "l4ci/rota" || data["changed"] != true {
		t.Fatalf("%+v", o)
	}
	if strings.Join(f.calls, "|") != "gh auth status|gh issue create -R l4ci/rota -t A learning -F -" {
		t.Fatalf("calls %q", f.calls)
	}
	// The old helper read the body with $(cat): trailing newlines go, CR stays.
	if f.stdins[1] != "learned this\r\nand that" {
		t.Fatalf("body %q", f.stdins[1])
	}

	// $ROTA_UPSTREAM_REPO, then --upstream-repo, wins over the default; - reads stdin.
	f = &forge{answer: created}
	useForge(t, f)
	t.Setenv("ROTA_UPSTREAM_REPO", "env/repo")
	trRun(t, dir, "from stdin", append([]string{"tracker", "suggest-upstream", "--title", "T", "--body-file", "-"}, ok...)...)
	o = trRun(t, dir, "x", append([]string{"tracker", "suggest-upstream", "--title", "T", "--body-file", "-", "--upstream-repo", "flag/repo"}, ok...)...)
	if !strings.Contains(f.calls[1], "-R env/repo") || !strings.Contains(f.calls[3], "-R flag/repo") || f.stdins[1] != "from stdin" {
		t.Fatalf("calls %q stdins %q", f.calls, f.stdins)
	}
	if o.stdout != "https://github.com/l4ci/rota/issues/123\n" {
		t.Fatalf("text output %q", o.stdout)
	}

	t.Setenv("ROTA_UPSTREAM_REPO", "")
	for _, c := range []struct {
		name   string
		answer func(string, []string) (string, string, int)
		found  bool
		args   []string
		code   int
		has    string
	}{
		{"no title", created, true, []string{"--body-file", body}, 2, "--title is required"},
		{"no body", created, true, []string{"--title", "T"}, 2, "--body-file is required"},
		{"unreadable body", created, true, []string{"--title", "T", "--body-file", filepath.Join(dir, "nope")}, 2, "--body-file"},
		{"no confirm", created, true, []string{"--title", "T", "--body-file", body}, 4, "manual gate 'public-filing' is not cleared"},
		{"confirm without note", created, true, []string{"--title", "T", "--body-file", body, "--confirm"}, 2, "--confirm-note"},
		{"note without confirm", created, true, []string{"--title", "T", "--body-file", body, "--confirm-note", "yes"}, 2, "--confirm-note"},
		{"gh missing", created, false, []string{"--title", "T", "--body-file", body}, 5, "https://github.com/l4ci/rota/issues/new"},
		{"not authed", func(string, []string) (string, string, int) { return "", "not logged in", 1 }, true,
			[]string{"--title", "T", "--body-file", body}, 5, "https://github.com/l4ci/rota/issues/new"},
		{"create fails", func(_ string, args []string) (string, string, int) {
			if args[0] == "auth" {
				return "", "", 0
			}
			return "", "HTTP 422", 1
		}, true, []string{"--title", "T", "--body-file", body}, 5, "HTTP 422"},
	} {
		f := &forge{answer: c.answer}
		useForge(t, f)
		f.found = c.found
		args := c.args
		if c.code == 5 {
			args = append(append([]string{}, args...), ok...)
		}
		o := trRun(t, dir, "", append([]string{"tracker", "suggest-upstream"}, args...)...)
		if c.code == 4 && len(f.calls) != 0 {
			t.Errorf("%s: gh ran before the gate refused: %q", c.name, f.calls)
		}
		if o.code != c.code || !strings.Contains(o.stderr, c.has) {
			t.Errorf("%s: exit %d, stderr %q; want %d with %q", c.name, o.code, o.stderr, c.code, c.has)
		}
	}
}
