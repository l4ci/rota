package verdict

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
)

func followUpIssues() (*backlog.Issues, *trackertest.Fake) {
	tr := &trackertest.Fake{}
	return &backlog.Issues{Cfg: jsonx.NewObject(), Tracker: tr, CountProof: func(string) int { return 0 }}, tr
}

func minors(titles ...string) []Finding {
	var fs []Finding
	for _, ti := range titles {
		fs = append(fs, Finding{Severity: "minor", Title: ti, File: ti + ".go"})
	}
	return fs
}

// The tracker path files, then appends to, the PR's one item.
func TestFileFollowUpsOnIssueBackend(t *testing.T) {
	be, tr := followUpIssues()
	root := t.TempDir() // one root, so both calls take the same per-PR lock file
	id, added, err := FileFollowUps(be, root, minors("one"), "#77", "review-quality", "FAIL")
	if err != nil || id == "" || added != 1 {
		t.Fatalf("first: %q %d %v", id, added, err)
	}
	id2, added, err := FileFollowUps(be, root, minors("one", "two"), "77", "review-quality", "FAIL") // `77` is `#77`
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

// The item body is the shape backlog's deferredDependency and the gate read:
// a Problem block per finding, the finding's fix (else the neutral line) as its criterion.
func TestFollowUpBodyShape(t *testing.T) {
	fs := []Finding{
		{Severity: "minor", Title: "nit", File: "a.go", Line: 4, Detail: "why", Fix: "rename it"},
		{Severity: "major", Title: "bare"},
	}
	be, tr := followUpIssues()
	if _, added, err := FileFollowUps(be, t.TempDir(), fs, "#9", "review-spec", "FAIL"); err != nil || added != 2 {
		t.Fatalf("%d %v", added, err)
	}
	body := tr.Issues[0].Body
	for _, want := range []string{
		"### nit\n\nwhy\n\nWhere: `a.go:4`\n\nDeferred minor finding from the review-spec FAIL verdict on #9.",
		"- [ ] AC-1: rename it\n", "- [ ] AC-2: " + neutralAC + "\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	// A bare finding (no File) has no Where line and dedups on its title alone.
	bare := body[strings.Index(body, "### bare"):]
	if strings.Contains(bare, "Where:") {
		t.Errorf("bare finding has a Where line:\n%s", bare)
	}
	if _, added, err := FileFollowUps(be, t.TempDir(), fs[1:], "#9", "review-spec", "FAIL"); err != nil || added != 0 {
		t.Errorf("refiled bare finding: added %d, %v, want 0", added, err)
	}
}

// Dedup keys on title+file: the same title in another file is a new finding.
func TestFileFollowUpsDedupSameTitleDifferentFile(t *testing.T) {
	be, tr := followUpIssues()
	root := t.TempDir()
	a := Finding{Severity: "minor", Title: "nit", File: "a.go"}
	b := Finding{Severity: "minor", Title: "nit", File: "b.go"}
	id, added, err := FileFollowUps(be, root, []Finding{a}, "#5", "review-quality", "FAIL")
	if err != nil || added != 1 {
		t.Fatalf("first: %q %d %v", id, added, err)
	}
	id2, added, err := FileFollowUps(be, root, []Finding{a, b}, "#5", "review-quality", "FAIL")
	if err != nil || id2 != id || added != 1 {
		t.Fatalf("second: %q %d %v, want %q and 1 new line", id2, added, err, id)
	}
	body := tr.Issues[0].Body
	if strings.Count(body, "### nit") != 2 || !strings.Contains(body, "`a.go`") || !strings.Contains(body, "`b.go`") {
		t.Errorf("body lacks both files:\n%s", body)
	}
}

func TestRelatesTo(t *testing.T) {
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
}

func TestDeferredAbove(t *testing.T) {
	fs := []Finding{
		{Severity: "blocker", Title: "b"}, {Severity: "major", Title: "m"},
		{Severity: "minor", Title: "n"}, {Severity: "info", Title: "i"},
	}
	titles := func(floor string) string {
		var out []string
		for _, f := range DeferredAbove(fs, floor) {
			out = append(out, f.Title)
		}
		return strings.Join(out, ",")
	}
	if got := titles(FloorMinor); got != "n" {
		t.Errorf("minor floor kept %q", got)
	}
	if got := titles(FloorOff); got != "" {
		t.Errorf("off floor kept %q", got)
	}
}

// Verdicts recorded together for one PR still end in one item holding every finding.
func TestFileFollowUpsConcurrent(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	for f, c := range map[string]string{"BACKLOG.md": "# TODO\n\n## Tasks\n\n## Completed\n", "counters.json": "{}\n"} {
		if err := os.WriteFile(filepath.Join(dir, ".rota", f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	be := &backlog.File{Root: dir}
	var wg sync.WaitGroup
	names := []string{"alpha", "beta", "gamma", "delta"}
	for _, n := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := FileFollowUps(be, dir, minors(n), "#77", "review-quality", "FAIL"); err != nil {
				t.Errorf("%s: %v", n, err)
			}
		}()
	}
	wg.Wait()
	items, err := be.List(true)
	if err != nil || len(items) != 1 {
		t.Fatalf("%d items, %v; want 1", len(items), err)
	}
	text, _, _ := be.Detail(items[0].ID)
	for _, n := range names {
		if !strings.Contains(text, "### "+n) {
			t.Errorf("item lacks %s:\n%s", n, text)
		}
	}
}

func TestDeferredAboveMajorFloor(t *testing.T) {
	fs := []Finding{{Severity: "major", Title: "m", Blocking: new(bool)}, {Severity: "minor", Title: "n"}}
	got := DeferredAbove(fs, FloorMajor)
	if len(got) != 1 || got[0].Title != "m" {
		t.Errorf("major floor kept %v", got)
	}
}
