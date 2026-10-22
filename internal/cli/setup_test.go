package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
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
	// No harness or host on PATH, so the project signals do not depend on the machine.
	d.LookPath = func(string) (string, error) { return "", exec.ErrNotFound }
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
	// default autonomy, review none, qa true, worker and orchestrator harness unset
	in := "1\nworktree\n2\n\n\nnone\n2\n\n\n\n\n\n"
	code, _, errOut := setupRun(t, dir, in, true)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	for k, want := range map[string]any{
		"issues.source": "file", "work.isolation": "worktree", "work.mergeStrategy": "pr",
		"round.dispatch": "subagent", "autonomy.level": "off", "ship.review": "none", "ship.qa": true,
	} {
		if got := cfgValue(t, dir, k); got != want {
			t.Errorf("%s = %v, want %v", k, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Error("init did not run the blocks:", err)
	}
	if !strings.Contains(errOut, "Where do the issues live?") || !strings.Contains(errOut, "(default)") {
		t.Errorf("prompts missing:\n%s", errOut)
	}
}

// The harness questions write only what was chosen: Enter leaves the key
// unset, an answer writes it.
func TestSetupHarnessQuestionsWriteOnlyAnswers(t *testing.T) {
	skipped := t.TempDir()
	if code, _, errOut := setupRun(t, skipped, "\n\n\n\n\n\n\n\n\n\n\n\n", true); code != 0 {
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
	if code, _, errOut := setupRun(t, chosen, "\n\n\n\n\n\n\ncodex\n4\n\n\n\n", true); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := cfgValue(t, chosen, "round.workerKind"); got != harness.Codex {
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

func TestSetupIssueSourceNamesTheTracker(t *testing.T) {
	dir := t.TempDir()
	// issues.source gitlab (3rd choice), then defaults for the rest
	code, _, errOut := setupRun(t, dir, "3\n\n\n\n\n\n\n\n\n\n\n\n", true)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := cfgValue(t, dir, "issues.source"); got != "gitlab" {
		t.Errorf("issues.source = %v", got)
	}
	if strings.Contains(errOut, "Which tracker holds the issues?") {
		t.Error("the provider question is gone: issues.source names the tracker")
	}
}

func TestSetupRepromptsOnBadAnswer(t *testing.T) {
	dir := t.TempDir()
	code, _, errOut := setupRun(t, dir, "nope\n9\n\n\n\n\n\n\n\n\n\n\n\n\n", true)
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
		{"--yes", "--set", "issues.provider=github"}, // replaced by issues.source
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

func globalFile(t *testing.T, gdir string) string {
	t.Helper()
	raw, err := os.ReadFile(config.GlobalPath(gdir))
	if err != nil {
		return ""
	}
	return string(raw)
}

// AC-1, AC-3, AC-5: first use lists the answers, saves them globally on y,
// and never writes a project-owned key.
func TestSetupOffersSaveGlobalOnFirstUse(t *testing.T) {
	gdir := isolateGlobal(t)
	dir := t.TempDir()
	in := "1\nworktree\n2\n\n\nnone\n2\n\n\n\n\n\ny\n"
	code, _, errOut := setupRun(t, dir, in, true)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if !strings.Contains(errOut, "every project that does not set the key itself") {
		t.Errorf("save offer must say the answers apply to every project, got:\n%s", errOut)
	}
	if !strings.Contains(errOut, "Save as global defaults?") || !strings.Contains(errOut, "work.isolation = worktree") {
		t.Errorf("offer missing:\n%s", errOut)
	}
	g := globalFile(t, gdir)
	if !strings.Contains(g, `"worktree"`) || !strings.Contains(g, `"issues"`) {
		t.Errorf("global config lacks the answers:\n%s", g)
	}
	for _, bad := range []string{"baseBranch", "umbrella", "rotaVersion", "version"} {
		if strings.Contains(g, bad) {
			t.Errorf("global holds project-owned %q:\n%s", bad, g)
		}
	}
	proj, _ := os.ReadFile(filepath.Join(dir, ".rota", "config.json"))
	if !strings.Contains(string(proj), "worktree") {
		t.Errorf("project config lost the answers:\n%s", proj)
	}
}

func TestSetupSaveGlobalDeclinedWritesNothing(t *testing.T) {
	gdir := isolateGlobal(t)
	in := "1\nworktree\n2\n\n\nnone\n2\n\n\n\n\n\nn\n"
	if code, _, e := setupRun(t, t.TempDir(), in, true); code != 0 {
		t.Fatalf("exit %d: %s", code, e)
	}
	if g := globalFile(t, gdir); g != "" {
		t.Errorf("declined offer wrote the global config:\n%s", g)
	}
}

// AC-2: with a global config, only differing answers are offered and merged.
func TestSetupOffersOnlyDifferingAnswers(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{"work":{"isolation":"worktree"},"issues":{"source":"file"},"ship":{"qa":false}}`)
	dir := t.TempDir()
	// issues.source file (same), isolation worktree (default, same), pr, rest default, qa true (differs)
	in := "\n\n2\n\n\nnone\n2\n\n\n\n\n\ny\n"
	code, _, errOut := setupRun(t, dir, in, true)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	offer := errOut[strings.Index(errOut, "differ from your global"):]
	if strings.Contains(offer, "work.isolation") || strings.Contains(offer, "issues.source") {
		t.Errorf("unchanged answers were offered:\n%s", offer)
	}
	if !strings.Contains(offer, "ship.qa = true") {
		t.Errorf("changed answer not offered:\n%s", offer)
	}
	g := globalFile(t, gdir)
	if !strings.Contains(g, `"qa": true`) || !strings.Contains(g, `"isolation": "worktree"`) {
		t.Errorf("merge lost or missed keys:\n%s", g)
	}
}

// AC-4: no prompt without a terminal or with --yes; --save-global saves silently.
func TestSetupSaveGlobalNonInteractive(t *testing.T) {
	gdir := isolateGlobal(t)
	if code, _, e := setupRun(t, t.TempDir(), "", false, "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, e)
	}
	if g := globalFile(t, gdir); g != "" {
		t.Errorf("--yes saved globally:\n%s", g)
	}
	code, _, errOut := setupRun(t, t.TempDir(), "", false, "--set", "work.isolation=worktree", "--save-global")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.Contains(errOut, "Save as global") {
		t.Errorf("--save-global prompted:\n%s", errOut)
	}
	if g := globalFile(t, gdir); !strings.Contains(g, `"worktree"`) {
		t.Errorf("--save-global did not save:\n%s", g)
	}
}

// A corrupt global config warns and skips the save offer: no all-answers
// list, no failing save after the project config is written.
func TestSetupCorruptGlobalSkipsOffer(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `not json`)
	in := "1\nworktree\n2\n\n\nnone\n2\n\n\n\n\n\ny\n"
	code, _, errOut := setupRun(t, t.TempDir(), in, true)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.Contains(errOut, "Save as global defaults?") {
		t.Errorf("offer shown for a corrupt global config:\n%s", errOut)
	}
	if !strings.Contains(errOut, "warning") {
		t.Errorf("no warning:\n%s", errOut)
	}
	code, _, errOut = setupRun(t, t.TempDir(), "", false, "--set", "work.isolation=worktree", "--save-global")
	if code != 0 {
		t.Fatalf("--save-global exit %d: %s", code, errOut)
	}
}

// A pre-0.21 global auto (no stamp) is one-hop: setup --yes must not pin it as
// a current project auto.
func TestSetupYesMapsStaleGlobalAutoToRoundtrip(t *testing.T) {
	gdir := isolateGlobal(t)
	writeFile(t, config.GlobalPath(gdir), `{"autonomy":{"level":"auto"}}`)
	dir := t.TempDir()
	if code, _, errOut := setupRun(t, dir, "", false, "--yes"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if got := cfgValue(t, dir, "autonomy.level"); got != "roundtrip" {
		t.Errorf("project autonomy.level = %v, want roundtrip", got)
	}
}
