package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
)

// setupRun runs `rota -C dir setup args` with stdin as given and, when tty,
// pretends stdin is a terminal.
func setupRun(t *testing.T, dir, stdin string, tty bool, args ...string) (int, string, string) {
	t.Helper()
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	d := testDeps()
	d.SetupIsTTY = func(io.Reader) bool { return tty }
	var so, se bytes.Buffer
	code := mainWith(d, append([]string{"-C", dir, "setup"}, args...), strings.NewReader(stdin), &so, &se)
	return code, so.String(), se.String()
}

func cfgValue(t *testing.T, dir, key string) any {
	t.Helper()
	v, err := config.Value(config.Load(filepath.Join(dir, ".rota", "config.json")), key)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSetupInteractiveAppliesAnswers(t *testing.T) {
	dir := t.TempDir()
	// 1 file backend (by number), worktree by name, pr by number, default dispatch,
	// default autonomy, review false, qa true, worker and orchestrator harness unset
	in := "1\nworktree\n2\n\n\nfalse\n2\n\n\n"
	code, _, errOut := setupRun(t, dir, in, true)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for k, want := range map[string]any{
		"backlog.backend": "file", "work.isolation": "worktree", "work.mergeStrategy": "pr",
		"work.dispatch": "subagent", "autonomy.level": "off", "ship.review": false, "ship.qa": true,
	} {
		if got := cfgValue(t, dir, k); got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Error("init did not run the blocks:", err)
	}
	if !strings.Contains(errOut, "Where does the backlog live?") || !strings.Contains(errOut, "(default)") {
		t.Errorf("prompts missing:\n%s", errOut)
	}
}

// The harness questions write only what was chosen: Enter leaves the key
// unset, an answer writes it.
func TestSetupHarnessQuestionsWriteOnlyAnswers(t *testing.T) {
	skipped := t.TempDir()
	if code, _, errOut := setupRun(t, skipped, "\n\n\n\n\n\n\n\n\n", true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	raw, err := os.ReadFile(filepath.Join(skipped, ".rota", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "workerKind") || strings.Contains(string(raw), "harness") {
		t.Errorf("an unanswered harness question wrote a key:\n%s", raw)
	}
	chosen := t.TempDir()
	if code, _, errOut := setupRun(t, chosen, "\n\n\n\n\n\n\ncodex\n4\n", true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := cfgValue(t, chosen, "round.workerKind"); got != "codex" {
		t.Errorf("round.workerKind = %v", got)
	}
	if got := cfgValue(t, chosen, "orchestrator.harness"); got != "opencode" {
		t.Errorf("orchestrator.harness = %v", got)
	}
	// Off a terminal, --yes keeps the defaults and writes neither.
	yes := t.TempDir()
	if code, _, errOut := setupRun(t, yes, "", false, "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if raw, _ := os.ReadFile(filepath.Join(yes, ".rota", "config.json")); strings.Contains(string(raw), "workerKind") {
		t.Errorf("--yes wrote round.workerKind:\n%s", raw)
	}
}

func TestSetupIssuesBackendAsksProvider(t *testing.T) {
	dir := t.TempDir()
	// backend issues, provider gitlab, then defaults for the rest (6 Enters)
	code, _, errOut := setupRun(t, dir, "2\n3\n\n\n\n\n\n\n\n\n", true)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := cfgValue(t, dir, "issues.provider"); got != "gitlab" {
		t.Errorf("issues.provider = %v", got)
	}
	if !strings.Contains(errOut, "Which tracker holds the issues?") {
		t.Error("provider question not asked")
	}
}

func TestSetupRepromptsOnBadAnswer(t *testing.T) {
	dir := t.TempDir()
	code, _, errOut := setupRun(t, dir, "nope\n9\n\n\n\n\n\n\n\n\n\n", true)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.Count(errOut, "not a choice") != 2 {
		t.Errorf("want two rejections:\n%s", errOut)
	}
}

func TestSetupEndOfInputWritesNothing(t *testing.T) {
	dir := t.TempDir()
	code, _, errOut := setupRun(t, dir, "1\n", true)
	if code != ExitUsage {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota")); err == nil {
		t.Error(".rota/ written after aborted setup")
	}
}

func TestSetupNonTTYNeverBlocks(t *testing.T) {
	dir := t.TempDir()
	code, _, errOut := setupRun(t, dir, "", false)
	if code != ExitUsage || !strings.Contains(errOut, "rota setup --list") || !strings.Contains(errOut, "--yes") {
		t.Errorf("exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota")); err == nil {
		t.Error("non-interactive setup wrote .rota/ without --yes")
	}
}

func TestSetupYesTakesDefaultsAndSet(t *testing.T) {
	dir := t.TempDir()
	// --yes beats a terminal: no prompt is read
	code, _, errOut := setupRun(t, dir, "", true, "--yes", "--set", "work.mergeStrategy=pr")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := cfgValue(t, dir, "work.mergeStrategy"); got != "pr" {
		t.Errorf("mergeStrategy = %v", got)
	}
	if got := cfgValue(t, dir, "work.isolation"); got != "branch" {
		t.Errorf("isolation = %v", got)
	}
	if strings.Contains(errOut, "Choose") {
		t.Error("--yes prompted")
	}
}

func TestSetupSetAloneNeedsNoYes(t *testing.T) {
	dir := t.TempDir()
	if code, _, errOut := setupRun(t, dir, "", false, "--set", "autonomy.level=auto"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := cfgValue(t, dir, "autonomy.level"); got != "auto" {
		t.Errorf("autonomy = %v", got)
	}
}

func TestSetupRejectsBadSetBeforeWriting(t *testing.T) {
	for _, args := range [][]string{
		{"--yes", "--set", "work.isolation=sideways"},
		{"--yes", "--set", "models.worker=opus"},
		{"--yes", "--set", "nokey"},
		{"--yes", "--set", "issues.provider=github"}, // backend is file
	} {
		dir := t.TempDir()
		code, _, errOut := setupRun(t, dir, "", false, args...)
		if code != ExitUsage {
			t.Errorf("%v: exit %d: %s", args, code, errOut)
		}
		if _, err := os.Stat(filepath.Join(dir, ".rota")); err == nil {
			t.Errorf("%v: wrote .rota/", args)
		}
	}
}

func TestSetupRefusesInitializedProject(t *testing.T) {
	dir := t.TempDir()
	if code, _, e := setupRun(t, dir, "", false, "--yes"); code != 0 {
		t.Fatal(e)
	}
	if code, _, errOut := setupRun(t, dir, "", false, "--yes"); code != ExitRefused {
		t.Errorf("exit %d: %s", code, errOut)
	}
}

func TestSetupListReadOnly(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := setupRun(t, dir, "", false, "--list", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	v, err := jsonx.Decode([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	qs, _ := v.(*jsonx.Object).Get("data")
	list, _ := qs.(*jsonx.Object).Get("questions")
	if len(list.([]any)) != len(config.Prompts) {
		t.Errorf("questions: %v", list)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota")); err == nil {
		t.Error("--list wrote .rota/")
	}
}

func TestSetupJSONNeverPrompts(t *testing.T) {
	dir := t.TempDir()
	code, out, errOut := setupRun(t, dir, "", true, "--yes", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	v, _ := jsonx.Decode([]byte(out))
	d, _ := v.(*jsonx.Object).Get("data")
	if _, ok := d.(*jsonx.Object).Get("answers"); !ok {
		t.Error("no answers in data")
	}
}

func TestSetupIsTTYRejectsNullAndPipes(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip(err)
	}
	defer null.Close()
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()
	for name, in := range map[string]io.Reader{"/dev/null": null, "pipe": r, "reader": strings.NewReader("")} {
		if testDeps().SetupIsTTY(in) {
			t.Errorf("%s counted as a terminal", name)
		}
	}
}
