package cli

import (
	"bytes"
	"fmt"
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
	// main role, 1 file backend (by number), worktree by name, pr by number, default dispatch,
	// default autonomy, review none, qa true, worker and orchestrator harness unset
	in := "\n1\nworktree\n2\n\n\nnone\n2\n\n\n\n\n\n"
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
	if code, _, errOut := setupRun(t, skipped, "\n\n\n\n\n\n\n\n\n\n\n\n\n", true); code != 0 {
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
	if code, _, errOut := setupRun(t, chosen, "\n\n\n\n\n\n\n\ncodex\n4\n\n\n\n", true); code != 0 {
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
	code, _, errOut := setupRun(t, dir, "\n3\n\n\n\n\n\n\n\n\n\n\n\n", true)
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
	code, _, errOut := setupRun(t, dir, "\nnope\n9\n\n\n\n\n\n\n\n\n\n\n\n\n", true)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	if strings.Count(errOut, "not a choice") != 2 {
		t.Errorf("want two rejections:\n%s", errOut)
	}
}

func TestSetupEndOfInputWritesNothing(t *testing.T) {
	dir := t.TempDir()
	code, _, errOut := setupRun(t, dir, "\n1\n", true)
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
	in := "\n1\nworktree\n2\n\n\nnone\n2\n\n\n\n\n\ny\n"
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
	in := "\n1\nworktree\n2\n\n\nnone\n2\n\n\n\n\n\nn\n"
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
	in := "\n\n\n2\n\n\nnone\n2\n\n\n\n\n\ny\n"
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
	in := "\n1\nworktree\n2\n\n\nnone\n2\n\n\n\n\n\ny\n"
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

// supportClone is a cloned project main has set up for a support machine:
// a tracker and round.support in the tracked config.
func supportClone(t *testing.T) (dir string, tracked []byte) {
	t.Helper()
	dir = trackerProject(t, `{"issues":{"source":"github"},"round":{"support":"mac"}}`+"\n")
	tracked, err := os.ReadFile(filepath.Join(dir, ".rota", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, tracked
}

func localConfig(t *testing.T, dir string) *jsonx.Object {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".rota", "config.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonx.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonx.Object)
}

// setupData is the data object of a --json envelope.
func setupData(t *testing.T, out string) *jsonx.Object {
	t.Helper()
	v, err := jsonx.Decode([]byte(out))
	if err != nil {
		t.Fatalf("not JSON: %v: %s", err, out)
	}
	d, _ := v.(*jsonx.Object).Get("data")
	o, ok := d.(*jsonx.Object)
	if !ok {
		t.Fatalf("no data object: %s", out)
	}
	return o
}

func assertTrackedUntouched(t *testing.T, dir string, tracked []byte) {
	t.Helper()
	if raw, _ := os.ReadFile(filepath.Join(dir, ".rota", "config.json")); !bytes.Equal(raw, tracked) {
		t.Errorf("config.json changed:\n%s", raw)
	}
}

// #1774 AC-2, AC-3: role support writes only config.local.json and asks only
// the machine-local questions.
func TestSetupSupportPath(t *testing.T) {
	t.Run("set and yes", func(t *testing.T) {
		dir, tracked := supportClone(t)
		writeLocalConfig(t, dir, `{"keep":true}`+"\n")
		code, out, errOut := setupRun(t, dir, "", false, "--json", "--yes", "--set", "round.role=support", "--set", "round.dispatch=tmux")
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		assertTrackedUntouched(t, dir, tracked)
		local := localConfig(t, dir)
		for k, want := range map[string]any{"round.role": "support", "round.dispatch": "tmux", "autonomy.level": "off", "keep": true} {
			if got, _ := config.Lookup(local, k); got != want {
				t.Errorf("local %s = %v, want %v", k, got, want)
			}
		}
		for _, k := range []string{"issues.source", "work.isolation", "ship.review", "round.workerKind"} {
			if _, ok := config.Lookup(local, k); ok {
				t.Errorf("local holds %s", k)
			}
		}
		if role, _ := setupData(t, out).Get("role"); role != "support" {
			t.Errorf("data.role = %v in %s", role, out)
		}
	})
	t.Run("interactive", func(t *testing.T) {
		isolateGlobal(t)
		dir, tracked := supportClone(t)
		// role support (2), then dispatch, autonomy, workerKind codex, harness.
		code, _, errOut := setupRun(t, dir, "2\n\n\ncodex\n\n", true)
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut)
		}
		assertTrackedUntouched(t, dir, tracked)
		if got, _ := config.Lookup(localConfig(t, dir), "round.workerKind"); got != harness.Codex {
			t.Errorf("local round.workerKind = %v", got)
		}
		for _, q := range []string{"Where do the issues live?", "How deep a review", "Which branch do the skills merge into?", "Save as global defaults?"} {
			if strings.Contains(errOut, q) {
				t.Errorf("support path asked %q:\n%s", q, errOut)
			}
		}
	})
	t.Run("main-only set refused", func(t *testing.T) {
		dir, tracked := supportClone(t)
		code, _, errOut := setupRun(t, dir, "", false, "--yes", "--set", "round.role=support", "--set", "issues.source=gitlab")
		if code != ExitUsage || !strings.Contains(errOut, "issues.source") {
			t.Errorf("exit %d: %s", code, errOut)
		}
		assertTrackedUntouched(t, dir, tracked)
	})
}

// #1774 AC-4: the support path refuses before asking when main has not named
// the support machine or the project has no tracker.
func TestSetupSupportPathRefuses(t *testing.T) {
	for _, tc := range []struct{ name, cfg, blockedBy, names string }{
		{"no support name", `{"issues":{"source":"github"}}`, "config", "round.support"},
		{"file backend", `{"round":{"support":"mac"}}`, "backend", "issues.source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := trackerProject(t, tc.cfg+"\n")
			code, out, errOut := setupRun(t, dir, "", false, "--json", "--set", "round.role=support")
			if code != ExitRefused {
				t.Fatalf("exit %d: %s %s", code, out, errOut)
			}
			if got, _ := setupData(t, out).Get("blockedBy"); got != tc.blockedBy {
				t.Errorf("blockedBy = %v", got)
			}
			if !strings.Contains(out, tc.names) {
				t.Errorf("refusal does not name %s: %s", tc.names, out)
			}
			if _, err := os.Stat(filepath.Join(dir, ".rota", "config.local.json")); err == nil {
				t.Error("refused setup wrote config.local.json")
			}
		})
	}
	// Outside a clone there is no tracked round.support either.
	empty := t.TempDir()
	if code, _, errOut := setupRun(t, empty, "", false, "--set", "round.role=support"); code != ExitRefused || !strings.Contains(errOut, "round.support") {
		t.Errorf("empty dir: exit %d: %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(empty, ".rota")); err == nil {
		t.Error("refused support setup ran init")
	}
}

// The main path keeps round.role out of both config files: main is the
// default, and the tracked config may not hold the key.
func TestSetupMainPathWritesNoRole(t *testing.T) {
	dir := t.TempDir()
	if code, _, errOut := setupRun(t, dir, "", false, "--yes", "--set", "round.role=main"); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".rota", "config.json"))
	if strings.Contains(string(raw), `"role"`) {
		t.Errorf("config.json holds round.role:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "config.local.json")); err == nil {
		t.Error("main path wrote config.local.json")
	}
}

// AC-3: --list says which role each question serves.
func TestSetupListRoles(t *testing.T) {
	code, out, errOut := setupRun(t, t.TempDir(), "", false, "--list", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	list, _ := setupData(t, out).Get("questions")
	roles := map[string]string{}
	for _, q := range list.([]any) {
		o := q.(*jsonx.Object)
		k, _ := o.Get("key")
		r, _ := o.Get("roles")
		roles[k.(string)] = fmt.Sprint(r)
	}
	for k, want := range map[string]string{
		"round.role": "[main support]", "round.dispatch": "[main support]", "autonomy.level": "[main support]",
		"round.workerKind": "[main support]", "orchestrator.harness": "[main support]",
		"issues.source": "[main]", "ship.review": "[main]", "test.fast": "[main]",
	} {
		if roles[k] != want {
			t.Errorf("%s roles = %q, want %q", k, roles[k], want)
		}
	}
	if first, _ := list.([]any)[0].(*jsonx.Object).Get("key"); first != "round.role" {
		t.Errorf("first question = %v, want round.role", first)
	}
	_, text, _ := setupRun(t, t.TempDir(), "", false, "--list")
	for _, line := range strings.Split(text, "\n") {
		key, _, _ := strings.Cut(line, " ")
		want := map[string]string{"round.role": "roles: both", "round.dispatch": "roles: both", "issues.source": "roles: main"}[key]
		if want != "" && !strings.Contains(line, want) {
			t.Errorf("--list line lacks %q: %s", want, line)
		}
	}
}
