package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/verdict"
)

// fileDeferredRepo is a file-backend repo with an empty backlog.
func fileDeferredRepo(t *testing.T) string {
	t.Helper()
	dir := verdictRepo(t)
	for f, c := range map[string]string{"BACKLOG.md": "# TODO\n\n## Tasks\n\n## Completed\n", "counters.json": "{}\n"} {
		if err := os.WriteFile(filepath.Join(dir, ".rota", f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func fileDeferred(t *testing.T, dir, kind, findings string) (int, map[string]any, string) {
	t.Helper()
	body := writeBody(t, t.TempDir(), `{"findings":[`+findings+`]}`)
	code, out, errOut := rotaIn(t, dir, "verdict", "add", "--kind", kind, "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77", "--json")
	if code != 0 {
		return code, nil, errOut
	}
	return code, data(t, out), errOut
}

func readTask(t *testing.T, dir, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".rota", "tasks", id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// #1599 AC-1/AC-4: several deferred findings make one item for the PR, one
// acceptance line each, the findings under Problem.
func TestFileDeferredOneItemPerPR(t *testing.T) {
	dir := fileDeferredRepo(t)
	code, d, errOut := fileDeferred(t, dir, "review-quality",
		`{"severity":"minor","title":"rename helper","file":"a.go","line":7,"detail":"foo is vague","fix":"helper is named bar"},{"severity":"major","title":"missing row","file":"b_test.go","blocking":false}`)
	if code != 0 {
		t.Fatalf("add: %d %s", code, errOut)
	}
	if filed, _ := d["filed"].([]any); len(filed) != 1 {
		t.Fatalf("filed = %v, want one item", d["filed"])
	}
	if d["appended"] != 2.0 {
		t.Errorf("appended = %v, want 2", d["appended"])
	}
	text := readTask(t, dir, "T01")
	for _, want := range []string{"## Problem", "rename helper", "a.go:7", "foo is vague", "missing row", "b_test.go", "#77",
		"- [ ] AC-1: helper is named bar", "- [ ] AC-2: the problem above no longer reproduces"} {
		if !strings.Contains(text, want) {
			t.Errorf("T01.md lacks %q:\n%s", want, text)
		}
	}
	bl, _ := os.ReadFile(filepath.Join(dir, ".rota", "BACKLOG.md"))
	if !strings.Contains(string(bl), "Review follow-ups from PR #77") {
		t.Errorf("title does not name the PR:\n%s", bl)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "tasks", "T02.md")); err == nil {
		t.Error("a second item was filed")
	}
}

// #1599 AC-2: a later call for the same PR appends; a repeated finding is not added twice.
func TestFileDeferredAppendsToOpenItem(t *testing.T) {
	dir := fileDeferredRepo(t)
	if code, _, errOut := fileDeferred(t, dir, "review-spec", `{"severity":"minor","title":"first","file":"a.go"}`); code != 0 {
		t.Fatalf("first: %d %s", code, errOut)
	}
	code, d, errOut := fileDeferred(t, dir, "review-quality",
		`{"severity":"minor","title":"first","file":"a.go"},{"severity":"minor","title":"second","file":"c.go"}`)
	if code != 0 {
		t.Fatalf("second: %d %s", code, errOut)
	}
	filed, _ := d["filed"].([]any)
	if len(filed) != 1 || d["appended"] != 1.0 {
		t.Fatalf("filed=%v appended=%v, want the existing item and 1 new line", d["filed"], d["appended"])
	}
	text := readTask(t, dir, "T01")
	if strings.Count(text, "- [ ] AC-") != 2 || !strings.Contains(text, "AC-2:") || strings.Count(text, "### first") != 1 || !strings.Contains(text, "### second") {
		t.Errorf("T01.md after append:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "tasks", "T02.md")); err == nil {
		t.Error("a second item was filed")
	}
	// Same call again: nothing new.
	code, d, _ = fileDeferred(t, dir, "review-quality", `{"severity":"minor","title":"second","file":"c.go"}`)
	if code != 0 || d["appended"] != 0.0 {
		t.Errorf("retry: %d appended=%v", code, d["appended"])
	}
}

// #1599 AC-3: the floor decides what is filed.
func TestFileDeferredFloor(t *testing.T) {
	for name, tc := range map[string]struct {
		floor     string
		wantLines float64
	}{"minor": {"minor", 2}, "major": {"major", 1}, "off": {"off", 0}} {
		t.Run(name, func(t *testing.T) {
			dir := fileDeferredRepo(t)
			if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"review":{"fileDeferred":"`+tc.floor+`"}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			code, d, errOut := fileDeferred(t, dir, "review-quality", `{"severity":"minor","title":"nit"},{"severity":"major","title":"big","blocking":false}`)
			if code != 0 {
				t.Fatalf("add: %d %s", code, errOut)
			}
			if d["appended"] != tc.wantLines {
				t.Errorf("appended = %v, want %v", d["appended"], tc.wantLines)
			}
			if tc.floor == "off" {
				if filed, _ := d["filed"].([]any); len(filed) != 0 {
					t.Errorf("filed = %v under off", filed)
				}
				if why, _ := d["filedWhy"].(string); !strings.Contains(why, "review.fileDeferred") {
					t.Errorf("filedWhy = %q, want it to name the key", why)
				}
			}
		})
	}
}

func TestConfigRefusesUnknownFileDeferred(t *testing.T) {
	dir := fileDeferredRepo(t)
	if code, _, _ := rotaIn(t, dir, "config", "set", "review.fileDeferred", "sometimes"); code == 0 {
		t.Error("unknown value accepted")
	}
	if code, _, errOut := rotaIn(t, dir, "config", "set", "review.fileDeferred", "major"); code != 0 {
		t.Errorf("major refused: %s", errOut)
	}
}

func followUpIssues() (*backlog.Issues, *trackertest.Fake) {
	tr := &trackertest.Fake{}
	return &backlog.Issues{Cfg: jsonx.NewObject(), Tracker: tr, CountProof: func(string) int { return 0 }}, tr
}

func testFollowUps(titles ...string) []followUp {
	var fs []followUp
	for _, ti := range titles {
		fs = append(fs, newFollowUp(verdict.Finding{Severity: "minor", Title: ti, File: ti + ".go"}, "#77", "review-quality", "FAIL"))
	}
	return fs
}

// #1599 relay 1: the tracker path files, then appends to, the PR's one item.
func TestFileFollowUpsOnIssueBackend(t *testing.T) {
	be, tr := followUpIssues()
	id, added, err := fileFollowUps(be, testFollowUps("one"), "#77")
	if err != nil || id == "" || added != 1 {
		t.Fatalf("first: %q %d %v", id, added, err)
	}
	id2, added, err := fileFollowUps(be, testFollowUps("one", "two"), "77") // `77` is `#77`
	if err != nil || id2 != id || added != 1 {
		t.Fatalf("second: %q %d %v, want %q and 1 new line", id2, added, err, id)
	}
	if len(tr.Issues) != 1 {
		t.Fatalf("%d issues, want 1", len(tr.Issues))
	}
	body := tr.Issues[0].Body
	if strings.Count(body, "- [ ] AC-") != 2 || !strings.Contains(body, "### two") || !strings.Contains(body, "Related: #77") {
		t.Errorf("body after append:\n%s", body)
	}
}

// #1599 relay 3: the PR matches by number, so `77` and `#77` reach one item and #7 is another.
func TestFollowUpPRMatchesByNumber(t *testing.T) {
	for _, tc := range []struct {
		related, pr string
		want        bool
	}{
		{"#77", "77", true}, {"77", "#77", true}, {"B1, #77", "https://github.com/o/r/pull/77", true},
		{"#771", "#77", false}, {"#7", "#77", false},
	} {
		if got := relatesTo(tc.related, tc.pr); got != tc.want {
			t.Errorf("relatesTo(%q, %q) = %v, want %v", tc.related, tc.pr, got, tc.want)
		}
	}
	dir := fileDeferredRepo(t)
	body := writeBody(t, t.TempDir(), `{"findings":[{"severity":"minor","title":"a"}]}`)
	for _, pr := range []string{"#77", "77"} {
		if code, _, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", pr); code != 0 {
			t.Fatalf("--pr %s: %d %s", pr, code, errOut)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "tasks", "T02.md")); err == nil {
		t.Error("`--pr 77` filed a second item beside `--pr #77`")
	}
}

// #1599 relay 2 and 4: an open follow-up with no Acceptance section is an error
// naming the item, not a silent no-op, and `filed` still names it.
func TestFileDeferredBrokenFollowUpReportsItem(t *testing.T) {
	dir := fileDeferredRepo(t)
	if code, _, errOut := fileDeferred(t, dir, "review-spec", `{"severity":"minor","title":"first"}`); code != 0 {
		t.Fatalf("first: %d %s", code, errOut)
	}
	if err := os.WriteFile(filepath.Join(dir, ".rota", "tasks", "T01.md"), []byte("## Problem\n\nhand edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := writeBody(t, t.TempDir(), `{"findings":[{"severity":"minor","title":"second"}]}`)
	code, out, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77", "--json")
	if code == 0 || !strings.Contains(errOut+out, "T01") {
		t.Fatalf("exit %d, want an error naming T01:\n%s\n%s", code, out, errOut)
	}
	if got := readTask(t, dir, "T01"); strings.Contains(got, "second") {
		t.Errorf("body changed despite the error:\n%s", got)
	}
}

// #1599 relay 6: verdicts recorded together for one PR still end in one item holding every finding.
func TestFileDeferredConcurrentVerdicts(t *testing.T) {
	dir := fileDeferredRepo(t)
	var wg sync.WaitGroup
	names := []string{"alpha", "beta", "gamma", "delta"}
	for _, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := writeBody(t, t.TempDir(), `{"findings":[{"severity":"minor","title":"`+n+`"}]}`)
			if code, _, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77"); code != 0 {
				t.Errorf("%s: %d %s", n, code, errOut)
			}
		}()
	}
	wg.Wait()
	text := readTask(t, dir, "T01")
	for _, n := range names {
		if !strings.Contains(text, "### "+n) {
			t.Errorf("T01.md lacks %s:\n%s", n, text)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "tasks", "T02.md")); err == nil {
		t.Error("a second item was filed")
	}
}

// #1599 relay 7: a floor that is not minor, major or off stops the call before the record is written.
func TestFileDeferredBadFloorStopsBeforeRecord(t *testing.T) {
	dir := fileDeferredRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"review":{"fileDeferred":"sometimes"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := fileDeferred(t, dir, "review-quality", `{"severity":"minor","title":"nit"}`); code == 0 {
		t.Fatal("a bad floor was accepted")
	}
	if code, out, _ := rotaIn(t, dir, "verdict", "show", "--json"); code == 0 && strings.Contains(out, "review-quality") {
		t.Errorf("a verdict was recorded despite the bad floor:\n%s", out)
	}
}
