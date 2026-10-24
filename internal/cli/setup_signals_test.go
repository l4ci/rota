package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
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

// signalOpts is the machine a setup run sees: binaries on PATH, environment,
// and what `gh|glab auth status` answers (exit code, or an exec error).
type signalOpts struct {
	onPath  []string
	env     map[string]string
	authErr error
	authRC  int
}

// signalRun runs `rota -C dir setup args` on a machine with gh authenticated
// and only the named binaries on PATH; ran lists every process the verb started.
func signalRun(t *testing.T, dir string, onPath []string, args ...string) (code int, out string, ran []string) {
	t.Helper()
	return signalRunWith(t, dir, signalOpts{onPath: onPath}, args...)
}

func signalGetenv(env map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := env[k]; ok {
			return v
		}
		if k == "TMUX" || k == "HERDR_ENV" {
			return ""
		}
		return os.Getenv(k) // the isolated global-config dir lives in the environment
	}
}

func signalRunWith(t *testing.T, dir string, o signalOpts, args ...string) (code int, out string, ran []string) {
	t.Helper()
	isolateGlobal(t)
	d := testDeps()
	d.SetupIsTTY = func(io.Reader) bool { return false }
	d.LookPath = func(name string) (string, error) {
		for _, n := range o.onPath {
			if n == name {
				return "/bin/" + n, nil
			}
		}
		return "", exec.ErrNotFound
	}
	d.Getenv = signalGetenv(o.env)
	d.Proc = func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		ran = append(ran, c.Name+" "+strings.Join(c.Args, " "))
		return proc.Result{ExitCode: o.authRC}, o.authErr
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
		"issues.source":   {"github", "github.com"},
		"git.baseBranch":  {"main", "main"},
		"test.fast":       {`["go test ./..."]`, "go.mod"},
		"test.full":       {`["go test ./..."]`, "go.mod"},
		"round.harnesses": {`["claude"]`, "claude"},
		"round.dispatch":  {"herdr", "herdr"},
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
	for _, key := range []string{"issues.source", "test.fast", "git.baseBranch", "round.harnesses"} {
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
	for k, want := range map[string]any{"issues.source": "gitlab", "git.baseBranch": "main", "round.harnesses": []any{"codex"}} {
		if got := cfgValue(t, dir, k); !reflect.DeepEqual(got, want) {
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
	// ten choice questions (the role first) at their defaults, then base branch, test.fast typed, test.full, envSetup and sharedDirs unset
	in := strings.Repeat("\n", 10) + "trunk\nmake check\n\n\n\n"
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

func sugg(t *testing.T, out, key string) (string, string, bool) {
	t.Helper()
	q := listQuestion(t, out, key)
	_, has := q.Get("suggestion")
	return str(q, "suggestion"), str(q, "evidence"), has
}

func TestSetupTestSignalsPerManifest(t *testing.T) {
	ci := ".github/workflows/ci.yml"
	for name, tc := range map[string]struct {
		files      map[string]string
		fast, full string
		evidence   string
	}{
		"package.json":                       {map[string]string{"package.json": `{"scripts":{"test":"jest"}}`}, `["npm test"]`, `["npm test"]`, "package.json"},
		"package.json without a test script": {map[string]string{"package.json": `{"scripts":{"build":"tsc"}}`}, "", "", ""},
		"pyproject":                          {map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\n"}, `["pytest"]`, `["pytest"]`, "pyproject.toml"},
		"cargo":                              {map[string]string{"Cargo.toml": "[package]\n"}, `["cargo test"]`, `["cargo test"]`, "Cargo.toml"},
		"makefile only":                      {map[string]string{"Makefile": "test:\n\techo hi\n"}, `["make test"]`, `["make test"]`, "Makefile"},
		"go beats package.json":              {map[string]string{"go.mod": "module x\n", "package.json": `{"scripts":{"test":"x"}}`}, `["go test ./..."]`, `["go test ./..."]`, "go.mod"},
		"makefile makes full":                {map[string]string{"go.mod": "module x\n", "Makefile": "test:\n\tgo test ./...\n"}, `["go test ./..."]`, `["make test"]`, "go.mod"},
		"ci line makes full":                 {map[string]string{"go.mod": "module x\n", "Makefile": "test:\n", ci: "jobs:\n  t:\n    steps:\n      - run: go test -race ./...\n"}, `["go test ./..."]`, `["go test -race ./..."]`, "go.mod"},
		"ci line without run key":            {map[string]string{"Cargo.toml": "x", ".gitlab-ci.yml": "test:\n  script:\n    - cargo test --all\n"}, `["cargo test"]`, `["cargo test --all"]`, "Cargo.toml"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for f, body := range tc.files {
				writeFile(t, filepath.Join(dir, f), body)
			}
			_, out, _ := signalRun(t, dir, nil, "--list", "--json")
			fast, ev, has := sugg(t, out, "test.fast")
			if fast != tc.fast || has != (tc.fast != "") {
				t.Errorf("test.fast = %q (has %v), want %q", fast, has, tc.fast)
			}
			if !strings.Contains(ev, tc.evidence) {
				t.Errorf("test.fast evidence %q lacks %q", ev, tc.evidence)
			}
			if full, fev, _ := sugg(t, out, "test.full"); full != tc.full {
				t.Errorf("test.full = %q, want %q", full, tc.full)
			} else if strings.Contains(name, "ci line") && !strings.Contains(fev, ":") {
				t.Errorf("test.full evidence %q should name file:line", fev)
			}
		})
	}
}

func TestSetupIssuesSourceSignal(t *testing.T) {
	for name, tc := range map[string]struct {
		remote string
		opts   signalOpts
		want   string // "" is no signal
		ev     string
	}{
		"scp form":           {"git@github.com:acme/x.git", signalOpts{onPath: []string{"gh"}}, "github", "github.com"},
		"ssh form":           {"ssh://git@gitlab.example.com/acme/x.git", signalOpts{onPath: []string{"glab"}}, "gitlab", "gitlab.example.com"},
		"https form":         {"https://github.com/acme/x", signalOpts{onPath: []string{"gh"}}, "github", "github.com"},
		"gh missing":         {"git@github.com:acme/x.git", signalOpts{}, "file", "gh is not installed"},
		"gh unauthenticated": {"git@github.com:acme/x.git", signalOpts{onPath: []string{"gh"}, authRC: 1}, "file", "not authenticated"},
		"probe fails":        {"git@github.com:acme/x.git", signalOpts{onPath: []string{"gh"}, authErr: context.DeadlineExceeded}, "", ""},
		"lookalike host":     {"git@notgitlab.evil.example:acme/x.git", signalOpts{onPath: []string{"glab"}}, "", ""},
		"other host":         {"git@git.example.com:acme/x.git", signalOpts{onPath: []string{"gh"}}, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			dir := signalRepo(t, tc.remote)
			_, out, ran := signalRunWith(t, dir, tc.opts, "--list", "--json")
			got, ev, has := sugg(t, out, "issues.source")
			if got != tc.want || has != (tc.want != "") {
				t.Errorf("issues.source = %q (has %v), want %q", got, has, tc.want)
			}
			if !strings.Contains(ev, tc.ev) {
				t.Errorf("evidence %q lacks %q", ev, tc.ev)
			}
			if tc.want == "github" || tc.want == "gitlab" {
				host := remoteHost(tc.remote)
				if len(ran) != 1 || !strings.HasSuffix(ran[0], "auth status --hostname "+host) {
					t.Errorf("probe = %v, want auth status --hostname %s", ran, host)
				}
			}
		})
	}
}

func TestSetupGlobalBeatsSignalAndSkipsProbe(t *testing.T) {
	dir := signalRepo(t, "git@github.com:acme/x.git")
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{"issues":{"source":"jira"},"round":{"dispatch":"subagent"}}`)
	d := testDeps()
	d.SetupIsTTY = func(io.Reader) bool { return false }
	d.LookPath = func(n string) (string, error) { return "/bin/" + n, nil }
	d.Getenv = signalGetenv(nil)
	var ran []string
	d.Proc = func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		ran = append(ran, c.Name)
		return proc.Result{}, nil
	}
	var so, se bytes.Buffer
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	if code := mainWith(d, []string{"-C", dir, "setup", "--list", "--json"}, strings.NewReader(""), &so, &se); code != 0 {
		t.Fatalf("exit %d: %s", code, se.String())
	}
	out := so.String()
	for key, want := range map[string]string{"issues.source": "jira", "round.dispatch": "subagent"} {
		q := listQuestion(t, out, key)
		if str(q, "default") != want {
			t.Errorf("%s default = %q, want the global %q", key, str(q, "default"), want)
		}
	}
	if ev := str(listQuestion(t, out, "round.dispatch"), "evidence"); !strings.Contains(ev, "project suggests herdr") {
		t.Errorf("dispatch evidence %q should note the herdr signal", ev)
	}
	if len(ran) != 0 {
		t.Errorf("probed %v although the global answers issues.source", ran)
	}
}

func TestSetupSetSkipsProbe(t *testing.T) {
	dir := signalRepo(t, "git@github.com:acme/x.git")
	code, out, ran := signalRunWith(t, dir, signalOpts{onPath: []string{"gh"}}, "--yes", "--set", "issues.source=file")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	if len(ran) != 0 {
		t.Errorf("probed %v although --set answers issues.source", ran)
	}
	if got := cfgValue(t, dir, "issues.source"); got != "file" {
		t.Errorf("issues.source = %v, want the --set value", got)
	}
}

func TestSetupWorktreeAndTmuxSignals(t *testing.T) {
	dir := signalRepo(t, "git@git.example.com:acme/x.git")
	if err := os.Mkdir(filepath.Join(dir, ".worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, out, _ := signalRunWith(t, dir, signalOpts{onPath: []string{"tmux"}, env: map[string]string{"TMUX": "/tmp/tmux-1/default,1,0"}}, "--list", "--json")
	if got, ev, _ := sugg(t, out, "work.isolation"); got != "worktree" || !strings.Contains(ev, ".worktrees") {
		t.Errorf("work.isolation = %q (%s)", got, ev)
	}
	if got, ev, _ := sugg(t, out, "round.dispatch"); got != "tmux" || !strings.Contains(ev, "$TMUX") {
		t.Errorf("round.dispatch = %q (%s)", got, ev)
	}
	// tmux on PATH without a session is no signal
	_, out, _ = signalRunWith(t, dir, signalOpts{onPath: []string{"tmux"}}, "--list", "--json")
	if _, _, has := sugg(t, out, "round.dispatch"); has {
		t.Error("tmux without $TMUX suggested a dispatch")
	}
}

func TestSetupFreeTextValidation(t *testing.T) {
	dir := t.TempDir()
	if code, _, _ := setupRun(t, dir, "", false, "--set", "git.baseBranch="); code != ExitUsage {
		t.Errorf("empty --set value: exit %d, want %d", code, ExitUsage)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota")); err == nil {
		t.Error("a refused --set wrote .rota/")
	}
	// a bare command is wrapped, JSON passes through
	if code, _, e := setupRun(t, dir, "", false, "--yes", "--set", "test.fast=make check", "--set", `test.full=["a","b"]`); code != 0 {
		t.Fatalf("exit %d: %s", code, e)
	}
	if got, _ := cfgValue(t, dir, "test.fast").([]any); len(got) != 1 || got[0] != "make check" {
		t.Errorf("test.fast = %v", got)
	}
	if got, _ := cfgValue(t, dir, "test.full").([]any); len(got) != 2 {
		t.Errorf("test.full = %v", got)
	}
}

func TestSetupDepSignalsPerManifest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		setup string
		dirs  string
	}{
		{"npm lock", []string{"package.json", "package-lock.json"}, "npm ci", `["node_modules"]`},
		{"pnpm lock", []string{"package.json", "pnpm-lock.yaml"}, "pnpm install --frozen-lockfile", `["node_modules"]`},
		{"yarn lock", []string{"package.json", "yarn.lock"}, "yarn install --frozen-lockfile", `["node_modules"]`},
		{"no lock", []string{"package.json"}, "npm install", `["node_modules"]`},
		{"python", []string{"pyproject.toml", "uv.lock"}, "uv sync", `[".venv"]`},
		{"cargo", []string{"Cargo.toml"}, "", ""},
		{"go", []string{"go.mod"}, "", ""},
	} {
		dir := t.TempDir()
		for _, f := range tc.files {
			os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o644)
		}
		setup, shared, ok := depSignals(dir)
		if (tc.setup != "") != ok || setup.Value != tc.setup || shared.Value != tc.dirs {
			t.Errorf("%s: got %q %q ok=%v, want %q %q", tc.name, setup.Value, shared.Value, ok, tc.setup, tc.dirs)
		}
		if ok && (setup.Evidence == "" || shared.Evidence == "") {
			t.Errorf("%s: a signal carries no evidence", tc.name)
		}
	}
}
