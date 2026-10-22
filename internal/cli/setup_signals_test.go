package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/proc"
)

// signalRepo is a git repo on main with an origin remote and a Go module.
func signalRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"},
		{"remote", "add", "origin", remote},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// signalRun runs `rota -C dir setup args` with PATH holding only the named
// binaries and gh authenticated; ran collects every process the verb started.
func signalRun(t *testing.T, dir string, onPath []string, args ...string) (code int, out string, ran []string) {
	t.Helper()
	d := testDeps()
	d.SetupIsTTY = func(io.Reader) bool { return false }
	d.LookPath = func(name string) (string, error) {
		for _, n := range onPath {
			if n == name {
				return "/bin/" + n, nil
			}
		}
		return "", exec.ErrNotFound
	}
	d.Getenv = func(string) string { return "" }
	d.Proc = func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		ran = append(ran, c.Name+" "+strings.Join(c.Args, " "))
		return proc.Result{}, nil
	}
	var so, se bytes.Buffer
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	code = mainWith(d, append([]string{"-C", dir, "setup"}, args...), strings.NewReader(""), &so, &se)
	return code, so.String(), ran
}

func listQuestion(t *testing.T, out, key string) *jsonx.Object {
	t.Helper()
	v, err := jsonx.Decode([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := v.(*jsonx.Object).Get("data")
	qs, _ := data.(*jsonx.Object).Get("questions")
	for _, q := range qs.([]any) {
		o := q.(*jsonx.Object)
		if k, _ := o.Get("key"); k == key {
			return o
		}
	}
	t.Fatalf("no question %s in %s", key, out)
	return nil
}

func str(o *jsonx.Object, k string) string {
	v, _ := o.Get(k)
	s, _ := v.(string)
	return s
}

func TestSetupListCarriesSuggestionAndEvidence(t *testing.T) {
	dir := signalRepo(t, "git@github.com:acme/x.git")
	code, out, ran := signalRun(t, dir, []string{"gh", "claude", "codex", "herdr"}, "--list", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for key, want := range map[string][2]string{
		"issues.source":    {"github", "github.com"},
		"git.baseBranch":   {"main", "main"},
		"test.fast":        {`["go test ./..."]`, "go.mod"},
		"test.full":        {`["go test ./..."]`, "go.mod"},
		"round.workerKind": {"claude", "claude"},
		"round.dispatch":   {"herdr", "herdr"},
	} {
		q := listQuestion(t, out, key)
		if got := str(q, "suggestion"); got != want[0] {
			t.Errorf("%s suggestion = %q, want %q", key, got, want[0])
		}
		if ev := str(q, "evidence"); !strings.Contains(ev, want[1]) {
			t.Errorf("%s evidence = %q, want it to name %q", key, ev, want[1])
		}
		if got := str(q, "default"); got != want[0] {
			t.Errorf("%s default = %q, want the suggestion %q", key, got, want[0])
		}
	}
	for _, c := range ran {
		if !strings.HasPrefix(c, "gh auth") {
			t.Errorf("signal reading started %q; only an auth probe may run", c)
		}
	}
}

func TestSetupNoSignalKeepsSchemaDefault(t *testing.T) {
	dir := t.TempDir()
	code, out, _ := signalRun(t, dir, nil, "--list", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, key := range []string{"issues.source", "test.fast", "git.baseBranch", "round.workerKind"} {
		q := listQuestion(t, out, key)
		if _, has := q.Get("suggestion"); has {
			t.Errorf("%s has a suggestion without a signal", key)
		}
	}
	if got := str(listQuestion(t, out, "issues.source"), "default"); got != "file" {
		t.Errorf("issues.source default = %q, want the schema default file", got)
	}
}

func TestSetupYesTakesSuggestions(t *testing.T) {
	dir := signalRepo(t, "https://gitlab.com/acme/x.git")
	code, out, _ := signalRun(t, dir, []string{"glab", "codex"}, "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for k, want := range map[string]any{"issues.source": "gitlab", "git.baseBranch": "main", "round.workerKind": "codex"} {
		if got := cfgValue(t, dir, k); got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}
	got, ok := cfgValue(t, dir, "test.fast").([]any)
	if !ok || len(got) != 1 || got[0] != "go test ./..." {
		t.Errorf("test.fast = %v, want [go test ./...]", cfgValue(t, dir, "test.fast"))
	}
}

func TestSetupInteractiveFreeTextWrapsBareCommand(t *testing.T) {
	dir := t.TempDir()
	// nine choice questions at their defaults, then base branch, test.fast typed, test.full unset
	in := strings.Repeat("\n", 9) + "trunk\nmake check\n\n"
	if code, _, errOut := setupRun(t, dir, in, true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := cfgValue(t, dir, "git.baseBranch"); got != "trunk" {
		t.Errorf("git.baseBranch = %v", got)
	}
	got, _ := cfgValue(t, dir, "test.fast").([]any)
	if len(got) != 1 || got[0] != "make check" {
		t.Errorf("test.fast = %v, want [make check]", got)
	}
	if got, _ := cfgValue(t, dir, "test.full").([]any); len(got) != 0 {
		t.Errorf("test.full = %v, want unset", got)
	}
}
