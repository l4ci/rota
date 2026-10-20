package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	gitIn(t, dir, "checkout", "-q", "-b", "feat/x")
	body := writeBody(t, t.TempDir(), findings)
	if code, _, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", v, "--body-file", body); code != 0 {
		t.Fatalf("verdict add: %d %s", code, errOut)
	}
	_, out, _ := rotaIn(t, dir, "verdict", "show", "--json")
	head = data(t, out)["head"].(string)
	gitIn(t, dir, "checkout", "-q", "main")
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
	if filed, _ := data(t, out)["filed"].([]any); len(filed) != 2 {
		t.Fatalf("filed = %v, want the minor and the waived major (not info, not blocking)", filed)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".rota", "BACKLOG.md"))
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
	for _, want := range []string{"## Acceptance", "- [ ] AC-1: rename the helper"} {
		if !strings.Contains(text, want) {
			t.Errorf("T01.md lacks %q:\n%s", want, text)
		}
	}
}

// A retry of the same recording files nothing new.
func TestVerdictAddFileDeferredIsIdempotent(t *testing.T) {
	dir := verdictRepo(t)
	for f, c := range map[string]string{"BACKLOG.md": "# TODO\n\n## Tasks\n\n## Completed\n", "counters.json": "{}\n"} {
		if err := os.WriteFile(filepath.Join(dir, ".rota", f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body := writeBody(t, t.TempDir(), mixedFindings)
	args := []string{"verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77", "--json"}
	_, out, _ := rotaIn(t, dir, args...)
	if filed, _ := data(t, out)["filed"].([]any); len(filed) != 2 {
		t.Fatalf("first call filed %v", filed)
	}
	before, _ := os.ReadFile(filepath.Join(dir, ".rota", "BACKLOG.md"))
	code, out, errOut := rotaIn(t, dir, args...)
	d := data(t, out)
	if filed, _ := d["filed"].([]any); code != 0 || len(filed) != 0 || d["skipped"] != 2.0 {
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
