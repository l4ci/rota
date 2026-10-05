package backlog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const viewBacklog = `# TODO

## Bugs
- **[B01] [P2] Two.** a Related: [F01] Milestone: M02
- **[B02] [P0] Zero.** b Milestone: M10, M2
- **[B03] Plain.** c
  - **[B04] [P1] Indented.** d

## Features
- **[F01] [Major] Big.** e Related: [B01]. Milestone: M02
- **[F02] [Cosmetic] Tiny.** f

## Tasks
- **[T01] Lone.** g

## Completed
- ~~**[B09] [P1] Done.** x~~ Done 2026-09-30 [` + "`abc`" + `]
`

func fileBackend(t *testing.T, md string) *File {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	if md != "" {
		if err := os.WriteFile(filepath.Join(root, ".rota", "BACKLOG.md"), []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &File{Root: root}
}

func rowIDs(rows []Row) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func TestOpenRowsFile(t *testing.T) {
	rows, md, ok, err := OpenRows(fileBackend(t, viewBacklog))
	if err != nil || !ok || md != viewBacklog {
		t.Fatalf("OpenRows = %v %v %v", ok, err, md == viewBacklog)
	}
	// Indented bullets count, as in the helpers; completed ones do not.
	if got := rowIDs(rows); !reflect.DeepEqual(got, []string{"B01", "B02", "B03", "B04", "F01", "F02", "T01"}) {
		t.Errorf("ids = %v", got)
	}
	if r := rows[0]; r.Tag != "P2" || r.Title != "Two" || r.Section != "Bugs" || r.Type != "B" || !strings.HasPrefix(r.Raw, "- **[B01]") {
		t.Errorf("row 0 = %+v", r)
	}
	if _, _, ok, err := OpenRows(fileBackend(t, "")); ok || err != nil {
		t.Errorf("missing BACKLOG.md: ok=%v err=%v", ok, err)
	}
}

func TestOpenRowsIssues(t *testing.T) {
	tr := &fakeTracker{Issues: []Issue{
		{Number: 12, Title: "Crash", State: "open", Labels: []string{"type:bug"}},
		{Number: 3, Title: "Idea", State: "open", Labels: []string{"type:feature"}},
		{Number: 7, Title: "Closed", State: "closed", Labels: []string{"type:bug"}},
	}}
	b := &Issues{Cfg: mustDecode(t, `{}`), Tracker: tr}
	rows, _, ok, err := OpenRows(b)
	if err != nil || !ok {
		t.Fatalf("OpenRows: %v %v", ok, err)
	}
	if got := rowIDs(rows); !reflect.DeepEqual(got, []string{"12", "3"}) {
		t.Fatalf("ids = %v", got)
	}
	if rows[0].Key != "B12" || rows[0].Type != "B" || rows[1].Key != "F3" || rows[1].Section != "Features" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestParseMilestones(t *testing.T) {
	if got := ParseMilestones("M01, M3 and m5 and M١٢"); !reflect.DeepEqual(got, []string{"M01", "M3", "M١٢"}) {
		t.Errorf("ParseMilestones = %q", got)
	}
	if got := ParseMilestones(""); got != nil {
		t.Errorf("empty = %q", got)
	}
}

func TestIDsByMilestoneAndMilestonesFor(t *testing.T) {
	rows, _, _, _ := OpenRows(fileBackend(t, viewBacklog))
	if got := IDsByMilestone(rows, "M02"); !reflect.DeepEqual(got, []string{"B01", "F01"}) {
		t.Errorf("M02 = %v", got)
	}
	if got := IDsByMilestone(rows, "M1"); len(got) != 0 {
		t.Errorf("M1 matched %v (substring of M10)", got)
	}
	if got := IDsByMilestone(rows, "M99"); got == nil || len(got) != 0 {
		t.Errorf("unknown milestone = %#v, want an empty list", got)
	}
	want := map[string]bool{"B01": true, "B02": true, "B03": true, "B99": true}
	got := MilestonesFor(rows, func(r Row) bool { return want[r.ID] })
	// Numeric order, unique: M2 and M02 are different strings with the same number.
	if !reflect.DeepEqual(got, []string{"M02", "M2", "M10"}) {
		t.Errorf("MilestonesFor = %v", got)
	}
}

func TestCountOpen(t *testing.T) {
	c := CountOpen(viewBacklog)
	if c["Bugs"] != 4 || c["Features"] != 2 || c["Tasks"] != 1 {
		t.Errorf("counts = %v", c)
	}
	// Any line that starts "- **[" counts, parsed or not.
	c = CountOpen("## Bugs\n- **[X1] t.** a\n   - **[B1] nested.**\n- plain\n")
	if c["Bugs"] != 2 {
		t.Errorf("loose counts = %v", c)
	}
}

func listing(t *testing.T, md string, active []Active, grep string) *Listing {
	t.Helper()
	rows, md, ok, err := OpenRows(fileBackend(t, md))
	if err != nil || !ok {
		t.Fatal(err)
	}
	return BuildListing(rows, md, active, grep)
}

func TestBuildListingOrderAndMarkdown(t *testing.T) {
	l := listing(t, viewBacklog, nil, "")
	if got := rowIDs(rowsOf(l.Bugs)); !reflect.DeepEqual(got, []string{"B02", "B04", "B01", "B03"}) { // P0, P1, P2, untagged
		t.Errorf("bugs = %v", got)
	}
	if got := rowIDs(rowsOf(l.Features)); !reflect.DeepEqual(got, []string{"F02", "F01"}) {
		t.Errorf("features = %v", got)
	}
	if !reflect.DeepEqual(l.Clusters, [][]string{{"B01", "F01"}}) {
		t.Errorf("clusters = %v", l.Clusters)
	}
	if l.Bugs[2].Related != "[F01]" || l.Features[1].Related != "[B01]" || l.Bugs[0].Milestone != "M10, M2" {
		t.Errorf("fields: %+v %+v", l.Bugs, l.Features)
	}
	want := `### Bugs

| ID | Prio | Title | Related | Milestone |
|----|----|----|----|----|
| B02 | P0 | Zero |  | M10, M2 |
| B04 | P1 | Indented |  |  |
| B01 | P2 | Two | [F01] | M02 |
| B03 |  | Plain |  |  |

### Features

| ID | Size | Title | Related | Milestone |
|----|----|----|----|----|
| F02 | Cosmetic | Tiny |  |  |
| F01 | Major | Big | [B01] | M02 |

### Tasks

| ID | Title | Related |
|----|----|----|
| T01 | Lone |  |

### Clusters

- [B01] Two ↔ [F01] Big

`
	if got := l.Text(); got != want {
		t.Errorf("Text:\n%s\nwant:\n%s", got, want)
	}
}

func rowsOf(lr []ListRow) []Row {
	out := make([]Row, len(lr))
	for i, r := range lr {
		out[i] = r.Row
	}
	return out
}

func TestBuildListingActiveAndGrep(t *testing.T) {
	active := []Active{{Branch: "feat/x", Items: []string{"B01", "B99"}, StartedAt: "2026-09-01T10:00:00Z"}}
	l := listing(t, viewBacklog, active, "")
	if got := rowIDs(rowsOf(l.Bugs)); !reflect.DeepEqual(got, []string{"B02", "B04", "B03"}) {
		t.Errorf("active item still in Bugs: %v", got)
	}
	if len(l.InProgress) != 2 || l.InProgress[0].Title != "[P2] Two" || l.InProgress[1].Title != "?" || l.InProgress[0].Type != "B" {
		t.Errorf("in progress = %+v", l.InProgress)
	}
	// Active items stay in the cluster graph.
	if !reflect.DeepEqual(l.Clusters, [][]string{{"B01", "F01"}}) {
		t.Errorf("clusters = %v", l.Clusters)
	}
	if !strings.Contains(l.Text(), "| ID | Title | Branch | Started |\n|----|-------|--------|---------|\n| B01 | [P2] Two | feat/x | 2026-09-01 |") {
		t.Errorf("text:\n%s", l.Text())
	}

	l = listing(t, viewBacklog, active, "zzz")
	if len(l.Bugs)+len(l.Features)+len(l.Tasks) != 0 || len(l.InProgress) != 2 || len(l.Clusters) != 0 {
		t.Errorf("grep zzz: %+v", l)
	}
	if strings.Contains(l.Text(), "No matches") { // the stream is still shown, so the list is not "empty"
		t.Errorf("text:\n%s", l.Text())
	}
	l = listing(t, viewBacklog, nil, "ZZZ")
	if !strings.HasSuffix(l.Text(), "No matches for pattern 'ZZZ'.\n") {
		t.Errorf("text = %q", l.Text())
	}
	if got := listing(t, "# TODO\n", nil, "").Text(); got != "Backlog empty. Run /rota-capture to add items.\n" {
		t.Errorf("empty text = %q", got)
	}
	// grep keeps a cluster with a matching member, drops others.
	l = listing(t, viewBacklog, nil, "tiny")
	if got := rowIDs(rowsOf(l.Features)); !reflect.DeepEqual(got, []string{"F02"}) || len(l.Clusters) != 0 {
		t.Errorf("grep tiny: %v %v", got, l.Clusters)
	}
	l = listing(t, viewBacklog, nil, "BIG")
	if len(l.Clusters) != 1 {
		t.Errorf("grep BIG: clusters = %v", l.Clusters)
	}
}

func TestBuildListingIssuesKeepSpellings(t *testing.T) {
	tr := &fakeTracker{Issues: []Issue{
		{Number: 12, Title: "Crash", State: "open", Labels: []string{"type:bug"}, Body: "<!-- rota:fields\nRelated: F3\n-->"},
		{Number: 3, Title: "Idea", State: "open", Labels: []string{"type:feature"}, Body: "<!-- rota:fields\nRelated: B12\n-->"},
	}}
	rows, md, _, err := OpenRows(&Issues{Cfg: mustDecode(t, `{}`), Tracker: tr})
	if err != nil {
		t.Fatal(err)
	}
	l := BuildListing(rows, md, nil, "")
	if len(l.Bugs) != 1 || l.Bugs[0].ID != "12" || len(l.Features) != 1 || l.Features[0].ID != "3" {
		t.Fatalf("rows: %+v %+v", l.Bugs, l.Features)
	}
	// Related cells keep the letter in the bullet; the cluster reports data IDs.
	if !reflect.DeepEqual(l.Clusters, [][]string{{"12", "3"}}) {
		t.Errorf("clusters = %v", l.Clusters)
	}
}

func TestTitleOf(t *testing.T) {
	md := "- **[B01] [P1] Tagged one.** x\n- **[T01] Task.** y\n- **[T02] No period**\n- ~~**[B09] [P2] Done. Really.** z~~ Done 2026-01-01 [`a`]\n"
	for id, want := range map[string]string{"B01": "[P1] Tagged one", "T01": "Task", "T02": "?", "B09": "[P2] Done", "B99": "?"} {
		if got := titleOf(md, id); got != want {
			t.Errorf("titleOf(%s) = %q, want %q", id, got, want)
		}
	}
}

func TestBuildSummary(t *testing.T) {
	f := fileBackend(t, viewBacklog)
	root := f.Root
	rota := filepath.Join(root, ".rota")
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(rota, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("KNOWLEDGE.md", "## A\n- x\n## B\n- y\n")
	write("ARCHIVE.md", "- ~~one~~\n- ~~two~~\n- other\n")
	write("status.json", `{"active":[{"items":["B01"],"branch":"b/x","startedAt":"2026-10-01T10:00:00Z"}]}`)
	items, err := f.List(true)
	if err != nil {
		t.Fatal(err)
	}

	sm := BuildSummary(root, items, true)
	if sm.Bugs != 3 || sm.Features != 2 || sm.Tasks != 1 {
		t.Errorf("counts = %d/%d/%d", sm.Bugs, sm.Features, sm.Tasks)
	}
	if len(sm.Recent) != 1 || sm.Recent[0].ID != "B09" || sm.Recent[0].ClosedAt != "2026-09-30" {
		t.Errorf("recent = %+v", sm.Recent)
	}
	if len(sm.Active) != 1 || sm.Active[0].Branch != "b/x" || sm.Active[0].Since != "2026-10-01" {
		t.Errorf("active = %+v", sm.Active)
	}
	if len(sm.Topics) != 1 || sm.Topics[0].Key != "knowledge" || sm.Topics[0].Count != 2 || !reflect.DeepEqual(sm.Topics[0].Shown, []string{"A", "B"}) {
		t.Errorf("topics = %+v", sm.Topics)
	}
	if sm.Archive != 2 {
		t.Errorf("archive = %d", sm.Archive)
	}
	if len(sm.Milestones) != 0 {
		t.Errorf("milestones = %+v", sm.Milestones)
	}
}
