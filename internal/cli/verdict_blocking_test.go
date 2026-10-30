package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/worker"
)

const mixedFindings = `{"findings":[
 {"severity":"blocker","title":"b"},
 {"severity":"major","title":"m"},
 {"severity":"minor","title":"n"},
 {"severity":"info","title":"i"},
 {"severity":"minor","title":"forced","blocking":true},
 {"severity":"major","title":"waived","blocking":false}]}`

// AC-1: blocking defaults from severity and an explicit value wins.
func TestVerdictAddBlockingDefaultsFromSeverity(t *testing.T) {
	dir := verdictRepo(t)
	body := writeBody(t, t.TempDir(), mixedFindings)
	code, out, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--json")
	if code != 0 {
		t.Fatalf("add: %d %s", code, errOut)
	}
	var got []bool
	for _, f := range data(t, out)["findings"].([]any) {
		got = append(got, f.(map[string]any)["blocking"] == true)
	}
	want := []bool{true, true, false, false, true, false}
	if len(got) != len(want) {
		t.Fatalf("findings: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("finding %d blocking = %v, want %v (all %v)", i, got[i], want[i], got)
		}
	}
	bad := writeBody(t, t.TempDir(), `{"findings":[{"severity":"minor","title":"x","blocking":"yes"}]}`)
	if code, _, _ := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", bad); code != 2 {
		t.Errorf("non-boolean blocking: exit %d, want 2", code)
	}
}

// bounceFixture seeds slot w1 on issue 31 and branch feat/x with a verdict at
// the branch tip; the head is returned.
func bounceFixture(t *testing.T, findings string) (dir, head string) {
	t.Helper()
	return bounceFixtureVerdict(t, findings, "CONCERNS")
}

func bounceFixtureVerdict(t *testing.T, findings, v string) (dir, head string) {
	t.Helper()
	dir = workerProject(t, `{"round":{"maxBounces":3}}`)
	gittest.Run(t, dir, "checkout", "-q", "-b", "feat/x")
	body := writeBody(t, t.TempDir(), findings)
	if code, _, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", v, "--body-file", body); code != 0 {
		t.Fatalf("verdict add: %d %s", code, errOut)
	}
	_, out, _ := rotaIn(t, dir, "verdict", "show", "--json")
	head = data(t, out)["head"].(string)
	gittest.Run(t, dir, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, ".rota", "workers.json"), []byte(`{"slots":[{"name":"w1","issue":"31","branch":"feat/x","state":"done"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, head
}

// AC-2: a bounce with no blocking finding in the current verdicts is refused.
func TestRoundBounceRefusesWithoutBlockingFindings(t *testing.T) {
	dir, head := bounceFixture(t, `{"findings":[{"severity":"minor","title":"nit"}]}`)

	code, out, errOut := rotaIn(t, dir, "round", "bounce", "31", "--head", head, "--json")
	d := data(t, out)
	if code != 4 || d["blockedBy"] != "no blocking findings" || d["changed"] != false {
		t.Fatalf("refusal: %d %v %s", code, d, errOut)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 0 {
		t.Errorf("a refused bounce must not count, registry says %d", n)
	}
	if code, _, errOut := rotaIn(t, dir, "round", "bounce", "31", "--head", head, "--force", "--json"); code != 0 {
		t.Fatalf("--force: %d %s", code, errOut)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 1 {
		t.Errorf("forced bounce must count, registry says %d", n)
	}
}

func TestRoundBounceCountsWithABlockingFinding(t *testing.T) {
	dir, head := bounceFixture(t, `{"findings":[{"severity":"minor","title":"nit"},{"severity":"major","title":"real"}]}`)
	if code, _, errOut := rotaIn(t, dir, "round", "bounce", "31", "--head", head, "--json"); code != 0 {
		t.Fatalf("blocking finding: %d %s", code, errOut)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 1 {
		t.Errorf("registry says %d bounces, want 1", n)
	}
}

// A verdict recorded at an older head does not judge the new one.
func TestRoundBounceIgnoresAStaleVerdict(t *testing.T) {
	dir, _ := bounceFixture(t, `{"findings":[{"severity":"minor","title":"nit"}]}`)
	if code, _, errOut := rotaIn(t, dir, "round", "bounce", "31", "--head", "deadbee", "--json"); code != 0 {
		t.Fatalf("stale verdict: %d %s", code, errOut)
	}
}

// AC-3: --file-deferred files each non-blocking finding as an item related to the PR.
func TestVerdictAddFilesDeferredFindings(t *testing.T) {
	dir := verdictRepo(t)
	floorMinor(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Tasks\n\n## Completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rota", "counters.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := writeBody(t, t.TempDir(), mixedFindings)

	if code, _, _ := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred"); code != 2 {
		t.Errorf("--file-deferred without --pr: exit %d, want 2", code)
	}
	code, out, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77", "--json")
	if code != 0 {
		t.Fatalf("add: %d %s", code, errOut)
	}
	d := data(t, out)
	if filed, _ := d["filed"].([]any); len(filed) != 1 || d["appended"] != 2.0 {
		t.Fatalf("filed = %v appended = %v, want one item with the minor and the waived major (not info, not blocking)", d["filed"], d["appended"])
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".rota", "tasks", "T01.md"))
	text := string(b)
	for _, want := range []string{"n", "waived", "#77"} {
		if !strings.Contains(text, want) {
			t.Errorf("BACKLOG.md lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "forced") {
		t.Errorf("a blocking finding was filed:\n%s", text)
	}
}

// AC-1 (#1019): a deferred item carries an Acceptance checkbox derived from the finding.
func TestVerdictAddFileDeferredWritesAcceptance(t *testing.T) {
	dir := verdictRepo(t)
	floorMinor(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Tasks\n\n## Completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rota", "counters.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := writeBody(t, t.TempDir(), `{"findings":[{"severity":"minor","title":"rename the helper","detail":"foo is misleading"}]}`)
	if code, _, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77"); code != 0 {
		t.Fatalf("add: %d %s", code, errOut)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".rota", "tasks", "T01.md"))
	text := string(b)
	for _, want := range []string{"## Acceptance", "- [ ] AC-1: the problem above no longer reproduces, and a test or check that failed before the fix passes after it"} {
		if !strings.Contains(text, want) {
			t.Errorf("T01.md lacks %q:\n%s", want, text)
		}
	}
}

// AC-1..4 (#1092): the finding is the Problem, never an acceptance line; a fix field becomes AC-1.
func TestVerdictAddFileDeferredProblemAndFix(t *testing.T) {
	for name, tc := range map[string]struct{ finding, wantAC string }{
		"without fix": {`{"severity":"minor","title":"rename the helper","detail":"foo is misleading","file":"a.go","line":7}`,
			"- [ ] AC-1: the problem above no longer reproduces, and a test or check that failed before the fix passes after it"},
		"with fix": {`{"severity":"minor","title":"rename the helper","detail":"foo is misleading","file":"a.go","line":7,"fix":"the helper is named bar"}`,
			"- [ ] AC-1: the helper is named bar"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := verdictRepo(t)
			floorMinor(t, dir)
			for f, c := range map[string]string{"BACKLOG.md": "# TODO\n\n## Tasks\n\n## Completed\n", "counters.json": "{}\n"} {
				if err := os.WriteFile(filepath.Join(dir, ".rota", f), []byte(c), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			body := writeBody(t, t.TempDir(), `{"findings":[`+tc.finding+`]}`)
			if code, _, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77"); code != 0 {
				t.Fatalf("add: %d %s", code, errOut)
			}
			b, _ := os.ReadFile(filepath.Join(dir, ".rota", "tasks", "T01.md"))
			text := string(b)
			for _, want := range []string{"## Problem", "rename the helper", "foo is misleading", "a.go:7", "#77", "review-quality FAIL", tc.wantAC} {
				if !strings.Contains(text, want) {
					t.Errorf("T01.md lacks %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "AC-1: rename the helper") || strings.Count(text, "- [ ]") != 1 {
				t.Errorf("title is an acceptance line, or AC count != 1:\n%s", text)
			}
		})
	}
}

// A retry of the same recording files nothing new.
func TestVerdictAddFileDeferredIsIdempotent(t *testing.T) {
	dir := verdictRepo(t)
	floorMinor(t, dir)
	for f, c := range map[string]string{"BACKLOG.md": "# TODO\n\n## Tasks\n\n## Completed\n", "counters.json": "{}\n"} {
		if err := os.WriteFile(filepath.Join(dir, ".rota", f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body := writeBody(t, t.TempDir(), mixedFindings)
	args := []string{"verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77", "--json"}
	_, out, _ := rotaIn(t, dir, args...)
	if d := data(t, out); d["appended"] != 2.0 {
		t.Fatalf("first call appended %v", d["appended"])
	}
	before, _ := os.ReadFile(filepath.Join(dir, ".rota", "BACKLOG.md"))
	code, out, errOut := rotaIn(t, dir, args...)
	d := data(t, out)
	if code != 0 || d["appended"] != 0.0 {
		t.Fatalf("retry: %d %v %s", code, d, errOut)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, ".rota", "BACKLOG.md")); string(after) != string(before) {
		t.Errorf("retry changed BACKLOG.md:\n%s", after)
	}
}

// A FAIL with no findings, or an item FAIL, is blocking: it may be bounced.
func TestRoundBounceCountsAFailWithoutFindings(t *testing.T) {
	for name, body := range map[string]string{
		"no findings": `{"findings":[]}`,
		"item fail":   `{"findings":[{"severity":"minor","title":"nit"}],"items":[{"id":"31","verdict":"FAIL"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir, head := bounceFixtureVerdict(t, body, "FAIL")
			if code, _, errOut := rotaIn(t, dir, "round", "bounce", "31", "--head", head, "--json"); code != 0 {
				t.Fatalf("%d %s", code, errOut)
			}
		})
	}
}

// AC-2 (#1019): a freshly filed deferred item passes the criteria check.
func TestVerdictAddFileDeferredItemIsReady(t *testing.T) {
	dir := verdictRepo(t)
	floorMinor(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Tasks\n\n## Completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rota", "counters.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := writeBody(t, t.TempDir(), `{"findings":[{"severity":"minor","title":"rename the helper"}]}`)
	if code, _, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77"); code != 0 {
		t.Fatalf("add: %d %s", code, errOut)
	}
	code, out, errOut := rotaIn(t, dir, "--json", "round", "candidates")
	if code != 0 {
		t.Fatalf("candidates: %d %s %s", code, out, errOut)
	}
	cs, _ := data(t, out)["candidates"].([]any)
	if len(cs) != 1 || cs[0].(map[string]any)["ready"] != true {
		t.Errorf("candidates = %s, want the filed item ready", out)
	}
}

// floorMinor sets review.fileDeferred to minor: the default files only major.
func floorMinor(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"review":{"fileDeferred":"minor"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A lens verdict's blocking finding lets a bounce through when the review
// verdicts hold only nits (#2064).
func TestRoundBounceCountsABlockingLensFinding(t *testing.T) {
	dir, head := bounceFixture(t, `{"findings":[{"severity":"minor","title":"nit"}]}`)
	body := writeBody(t, t.TempDir(), `{"findings":[{"severity":"major","title":"argument injection"}]}`)
	if code, _, errOut := rotaIn(t, dir, "verdict", "add", "feat/x", "--kind", "review-security", "--verdict", "FAIL", "--sha", head, "--body-file", body); code != 0 {
		t.Fatalf("lens verdict add: %d %s", code, errOut)
	}
	if code, out, errOut := rotaIn(t, dir, "round", "bounce", "31", "--head", head, "--json"); code != 0 {
		t.Fatalf("a blocking lens finding must allow the bounce: %d %v %s", code, data(t, out), errOut)
	}
	if n := worker.LoadRegistryTolerant(dir).Bounces("31"); n != 1 {
		t.Errorf("registry says %d bounces, want 1", n)
	}
}

// A review record at the branch head with a blocking finding refuses `worker
// gate` (also --check-only) and `worker train` even when its verdict is only
// CONCERNS, the rule `round bounce` applies. Non-blocking findings pass, and a
// newer record at a new head clears the refusal.
func TestWorkerGateRefusesBlockingFindingUnderConcerns(t *testing.T) {
	dir := workerProject(t, `{"ship":{"review":"none"},"test":{"full":["test -f feature.txt"]}}`)
	rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	wt := filepath.Join(dir, ".worktrees", "w1")
	write(t, filepath.Join(wt, "feature.txt"), "f")
	gittest.Run(t, wt, "add", "feature.txt")
	gittest.Run(t, wt, "commit", "-q", "-m", "feature")
	branch := strings.TrimSpace(gittest.Run(t, wt, "rev-parse", "--abbrev-ref", "HEAD"))
	merged := func() bool { _, err := os.Stat(filepath.Join(dir, "feature.txt")); return err == nil }
	add := func(findings string) {
		t.Helper()
		body := writeBody(t, t.TempDir(), findings)
		if code, _, errOut := rotaIn(t, dir, "verdict", "add", branch, "--kind", "review-spec", "--verdict", "CONCERNS", "--body-file", body); code != 0 {
			t.Fatalf("verdict add: %d %s", code, errOut)
		}
	}

	add(`{"findings":[{"severity":"minor","title":"nit"},{"severity":"major","title":"real"}]}`)
	for _, args := range [][]string{
		{"worker", "gate", "w1", "--base", "main", "--check-only"},
		{"worker", "gate", "w1", "--base", "main"},
		{"worker", "train", "w1", "--base", "main"},
	} {
		code, out, _ := rotaIn(t, dir, append([]string{"--json"}, args...)...)
		d := data(t, out)
		if code != 4 || d["blockedBy"] != "verdict" || d["verdict"] != "verdict-blocked" || d["kind"] != "review-spec" || d["changed"] != false {
			t.Fatalf("%v: %d %v", args, code, d)
		}
		if merged() {
			t.Fatalf("%v merged a branch with a blocking finding", args)
		}
	}

	add(`{"findings":[{"severity":"minor","title":"nit"},{"severity":"major","title":"waived","blocking":false}]}`)
	if code, out, _ := rotaIn(t, dir, "--json", "worker", "gate", "w1", "--base", "main", "--check-only"); code != 0 {
		t.Fatalf("non-blocking findings: %d %s", code, out)
	}

	// A new head the record was not made at: the old blocking record is stale.
	add(`{"findings":[{"severity":"blocker","title":"old"}]}`)
	write(t, filepath.Join(wt, "more.txt"), "m")
	gittest.Run(t, wt, "add", "more.txt")
	gittest.Run(t, wt, "commit", "-q", "-m", "more")
	if code, out, _ := rotaIn(t, dir, "--json", "worker", "gate", "w1", "--base", "main", "--check-only"); code != 0 {
		t.Fatalf("stale blocking record: %d %s", code, out)
	}
	add(`{"findings":[]}`)
	if code, out, _ := rotaIn(t, dir, "--json", "worker", "gate", "w1", "--base", "main"); code != 0 {
		t.Fatalf("newer clean record: %d %s", code, out)
	}
	if !merged() {
		t.Error("the cleared branch did not merge")
	}
}
