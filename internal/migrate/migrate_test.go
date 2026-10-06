package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

const migBacklog = `# TODO

## Bugs
- **[B01] [P1] First bug.** Something broke. Related: [F01], [B08] Milestone: M01 Since: abc1234
- **[B02] [P2] Second bug.** Other. Repos: web

## Features
- **[F01] [Major] Feature.** Body. Detail: ` + "`.rota/features/F01.md`" + ` Related: [B01]

## Tasks
- **[T01] Task.** Do it.

## Completed
- ~~**[B08] [P2] Done.** x~~ Done 2026-01-01 [` + "`abc`" + `]
`

func migProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	put := func(rel, text string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(".rota/BACKLOG.md", migBacklog)
	put(".rota/features/F01.md", "# F01\n\nbody text\n\n## Proof\n- ok\n\n## Log\nlog\n")
	put(".rota/designs/T01.md", "design of [B01] and F01\n")
	put(".rota/milestones/M01.md", "---\nid: M01\ntitle: Core\nstatus: active\ndepends: []\n---\n\n# M01 — Core\n\n## Goal\n\nBuild it.\n\nSee B01.\n")
	put(".rota/plans/M01-S01.md", "slice [B01] and T1\n")
	for rel, text := range files {
		put(rel, text)
	}
	return root
}

func newMig(t *testing.T, root string, apply bool, f *trackertest.MS) (Options, *[]string, *[]time.Duration) {
	t.Helper()
	var warns []string
	var sleeps []time.Duration
	return Options{Root: root, Apply: apply, Limit: -1, Cfg: jsonx.NewObject(),
		Tracker: func() (Tracker, error) {
			if f == nil {
				t.Fatal("the tracker was built")
			}
			return f, nil
		},
		Sleep: func(d time.Duration) { sleeps = append(sleeps, d) },
		Warn:  func(s string) { warns = append(warns, s) },
		Today: func() string { return "2026-10-02" }}, &warns, &sleeps
}

func TestMigratePreviewTouchesNothing(t *testing.T) {
	root := migProject(t, nil)
	o, warns, _ := newMig(t, root, false, nil)
	res, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, op := range res.Ops {
		got = append(got, op.Action+": "+op.Text)
	}
	want := []string{
		"create-milestone: M01 (active)",
		"set: milestone M01 status active",
		`create-issue: B01 → bug "First bug" [type:bug, p1] milestone M01 (Related not migrated: B08)`,
		`create-issue: B02 → bug "Second bug" [type:bug, p2]`,
		`create-issue: F01 → feature "Feature" [type:feature, size:Major]`,
		`create-issue: T01 → task "Task" [type:task]`,
		"rewrite: Related on B01: F01 → #?",
		"note: proof on F01",
		"rewrite: Related on F01: B01 → #?",
		"note: design on T01",
		"set: plan body of milestone M01",
		"note: plan:S01 on M01",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ops:\n%q\nwant\n%q", got, want)
	}
	if res.Total != 4 || res.Migrated != 0 || res.Changed {
		t.Errorf("total %d migrated %d changed %v", res.Total, res.Migrated, res.Changed)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "issue-map.json")); err == nil {
		t.Error("a preview wrote the map")
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".rota", "BACKLOG.md")); string(b) != migBacklog {
		t.Error("a preview changed BACKLOG.md")
	}
	if len(*warns) != 0 {
		t.Errorf("warnings %q", *warns)
	}
	if !strings.Contains(strings.Join(res.Lines, "\n"), "would-be map:") {
		t.Error("no would-be map")
	}
}

func TestMigrateApplyAndNoop(t *testing.T) {
	root := migProject(t, nil)
	f := &trackertest.MS{Fake: &trackertest.Fake{}}
	o, warns, _ := newMig(t, root, true, f)
	res, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Done || !res.Changed || res.Migrated != 4 {
		t.Errorf("done %v changed %v migrated %d", res.Done, res.Changed, res.Migrated)
	}
	// tracking issue first (#1), then B01..T01 (#2..#5)
	if len(f.Issues) != 5 || f.Issues[0].Title != "M01 — Core" || f.Issues[1].Milestone != "M01 — Core" {
		t.Fatalf("issues %+v", f.Issues)
	}
	if got := f.Issues[1].Body; !strings.Contains(got, "Related: [F4]") || !strings.Contains(got, "Since: abc1234") || !strings.Contains(got, "Related before migration (not migrated): B08") {
		t.Errorf("B01 body %q", got)
	}
	if len(f.Issues[3].Comments) != 1 || !strings.HasPrefix(f.Issues[3].Comments[0].Body, "<!-- rota:proof -->") {
		t.Errorf("F01 comments %+v", f.Issues[3].Comments)
	}
	if got := f.Issues[4].Comments[0].Body; !strings.Contains(got, "[B2]") && !strings.Contains(got, "F4") {
		t.Errorf("design not rewritten: %q", got)
	}
	if !slices.Contains(f.Issues[0].Labels, "status:active") {
		t.Errorf("milestone labels %v", f.Issues[0].Labels)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".rota", "BACKLOG.md")); !strings.HasPrefix(string(b), "> Frozen: this backlog moved to the issue tracker on 2026-10-02") {
		t.Errorf("not frozen: %q", b)
	}
	if len(*warns) != 0 {
		t.Errorf("warnings %q", *warns)
	}
	// a finished migration makes no tracker call and writes nothing
	calls := len(f.Calls)
	res, err = Run(o)
	if err != nil || res.Changed || len(f.Calls) != calls {
		t.Errorf("rerun: %v changed %v, %d new calls", err, res.Changed, len(f.Calls)-calls)
	}
	for _, l := range res.Lines {
		if !strings.HasPrefix(l, "skip ") && !strings.HasPrefix(l, "Next:") {
			t.Errorf("rerun printed %q", l)
		}
	}
}

func TestMigratePace(t *testing.T) {
	root := migProject(t, nil)
	f := &trackertest.MS{Fake: &trackertest.Fake{}}
	o, _, sleeps := newMig(t, root, true, f)
	o.Cfg = migCfg(t, `{"issues": {"bulkPaceMs": 250}}`)
	res, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	// the pause comes before every write but the first
	writes := 0
	for _, l := range res.Lines {
		for _, p := range opActions {
			if strings.HasPrefix(l, p.prefix) && !strings.HasPrefix(l, "skip ") {
				writes++
				break
			}
		}
	}
	if len(*sleeps) != writes-1 || len(*sleeps) == 0 {
		t.Fatalf("%d pauses for %d writes", len(*sleeps), writes)
	}
	for _, d := range *sleeps {
		if d != 250*time.Millisecond {
			t.Fatalf("pause %v", d)
		}
	}
	// no pace, no pause; a preview never pauses
	for _, cfg := range []string{`{"issues": {"bulkPaceMs": 0}}`, `{"issues": {"bulkPaceMs": -5}}`} {
		root = migProject(t, nil)
		o, _, sleeps = newMig(t, root, true, &trackertest.MS{Fake: &trackertest.Fake{}})
		o.Cfg = migCfg(t, cfg)
		if _, err := Run(o); err != nil || len(*sleeps) != 0 {
			t.Errorf("%s: %v, %d pauses", cfg, err, len(*sleeps))
		}
	}
	o, _, sleeps = newMig(t, migProject(t, nil), false, nil)
	o.Cfg = migCfg(t, `{"issues": {"bulkPaceMs": 250}}`)
	if _, err := Run(o); err != nil || len(*sleeps) != 0 {
		t.Errorf("preview paused %d times", len(*sleeps))
	}
}

func migCfg(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrateLimitResumes(t *testing.T) {
	root := migProject(t, nil)
	f := &trackertest.MS{Fake: &trackertest.Fake{}}
	o, _, _ := newMig(t, root, true, f)
	o.Limit = 2
	res, err := Run(o)
	if err != nil || res.Done || res.Migrated != 2 {
		t.Fatalf("first run: %v done %v migrated %d", err, res.Done, res.Migrated)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".rota", "BACKLOG.md")); string(b) != migBacklog {
		t.Error("froze before every item existed")
	}
	if last := res.Lines[len(res.Lines)-1]; !strings.HasPrefix(last, "2 items remaining;") {
		t.Errorf("last line %q", last)
	}
	for _, c := range f.Calls {
		if c.Method == "add_comment" || c.Method == "edit" && false {
			t.Errorf("notes written before every item existed: %+v", c)
		}
	}
	o.Limit = -1
	res, err = Run(o)
	if err != nil || !res.Done || res.Migrated != 4 {
		t.Fatalf("second run: %v done %v migrated %d", err, res.Done, res.Migrated)
	}
	if len(f.Issues) != 5 {
		t.Errorf("%d issues; a resume must not duplicate", len(f.Issues))
	}
}

func TestMigrateTrackerStopsKeepProgress(t *testing.T) {
	root := migProject(t, nil)
	f := &trackertest.MS{Fake: &trackertest.Fake{}}
	o, _, _ := newMig(t, root, true, f)
	// the notes fail after every item exists
	f.Fake.Fail = map[string]error{"add_comment": &tracker.Error{Kind: tracker.KindRateLimited, Code: 4, Message: "slow down"}}
	res, err := Run(o)
	var te *tracker.Error
	if !errors.As(err, &te) || te.Kind != tracker.KindRateLimited {
		t.Fatalf("err %v", err)
	}
	if res.Migrated != 4 || res.Done {
		t.Errorf("migrated %d done %v", res.Migrated, res.Done)
	}
	if got := strings.Join(res.Lines, "\n"); !strings.Contains(got, "rate limited: 4 of 4 items migrated") || !strings.Contains(got, "re-run to continue") {
		t.Errorf("lines %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".rota", "BACKLOG.md")); string(b) != migBacklog {
		t.Error("froze after a failure")
	}
	// resume finishes
	f.Fake.Fail = nil
	res, err = Run(o)
	if err != nil || !res.Done {
		t.Fatalf("resume: %v done %v", err, res != nil && res.Done)
	}
	// a plain failure stops with the other message
	root = migProject(t, nil)
	f = &trackertest.MS{Fake: &trackertest.Fake{}}
	o, _, _ = newMig(t, root, true, f)
	f.Fake.Fail = map[string]error{"create": errors.New("not a tracker error")}
	if _, err = Run(o); err == nil || errors.As(err, &te) {
		t.Errorf("non-tracker error: %v", err)
	}
	f.Fake.Fail = map[string]error{"create": &tracker.Error{Kind: tracker.KindFailed, Code: 1, Message: "boom"}}
	res, err = Run(o)
	if !errors.As(err, &te) || !strings.Contains(strings.Join(res.Lines, "\n"), "stopped on a tracker error") {
		t.Errorf("tracker failure: %v %q", err, res.Lines)
	}
}

func TestMigrateRefusals(t *testing.T) {
	o, _, _ := newMig(t, t.TempDir(), true, nil)
	if _, err := Run(o); !errors.Is(err, ErrNothingToMigrate) {
		t.Errorf("no backlog: %v", err)
	}
	root := migProject(t, map[string]string{".rota/repos.json": `{"repos": [{"name": "web", "path": "web"}]}`})
	o, _, _ = newMig(t, root, true, nil)
	if _, err := Run(o); !errors.Is(err, ErrUmbrellaMigrate) {
		t.Errorf("umbrella: %v", err)
	}
	root = migProject(t, map[string]string{".rota/issue-map.json": "[]"})
	o, _, _ = newMig(t, root, true, nil)
	if _, err := Run(o); !errors.Is(err, ErrBadMap) {
		t.Errorf("array map: %v", err)
	}
	// a map that does not parse starts over
	root = migProject(t, map[string]string{".rota/issue-map.json": "{oops"})
	o, _, _ = newMig(t, root, false, nil)
	if _, err := Run(o); err != nil {
		t.Errorf("unparseable map: %v", err)
	}
}

func TestMigrateWarnings(t *testing.T) {
	root := migProject(t, map[string]string{".rota/BACKLOG.md": "# TODO\n\n## Bugs\n- **[B01] [P9] Odd tag.** a Milestone: M07\n- **[B02] [P1] Fine.** b Milestone: M09\n"})
	f := &trackertest.MS{Fake: &trackertest.Fake{Milestones: []string{"M09 — Shipped"}}}
	o, warns, _ := newMig(t, root, true, f)
	if _, err := Run(o); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"B01: tag [P9] is not valid for bugs; dropped",
		"B01: milestone M07 is not on the tracker (shipped/archived milestones are not migrated); Milestone field dropped",
	}
	if !reflect.DeepEqual(*warns, want) {
		t.Errorf("warnings %q", *warns)
	}
	if f.Issues[2].Milestone != "M09 — Shipped" {
		t.Errorf("M09 not attached: %+v", f.Issues)
	}
}

func TestTokenMatches(t *testing.T) {
	cases := map[string][]string{
		"[B01] and F2, T3.":              {"B01", "F2", "T3"},
		"xB01 B01x B011 B01.md /B01 -F2": {"B011"},
		"B1 B1 (T9)":                     {"B1", "B1", "T9"},
		"M01 B":                          nil,
		"éB1 _B1 1B1":                    nil,
		"B١٢ end":                        {"B١٢"},
		"B01.mdx":                        nil,
		"B01.md":                         nil,
		"B01.m":                          {"B01"},
	}
	for in, want := range cases {
		var got []string
		for _, m := range tokenMatches(in) {
			got = append(got, m.id)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestScanImported(t *testing.T) {
	root := t.TempDir()
	put := func(rel, text string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(".rota/BACKLOG.md", "# T\n\n## Bugs\n- **[B01] [P1] A.** GH: #5 GL:#6 Repos: web, api Related: [F01]\n- **[B02] [P1] B.** GH:   #7\n\n## Completed\n- ~~**[B03] [P1] C.** GH: #8~~ Done 2026-01-01 [`a`]\n")
	put(".rota/ARCHIVE.md", "## Old\n- ~~**[B01] [P1] A.** GH: #5 Repos: web Related: [F01]~~ Done 2026-01-01 [`a`]\n- ~~**[F05] [Minor] D.** GL: #9~~ Done 2026-01-01 [`a`]\n")
	put(".rota/bugs/B02.md", "GH: #7\nGH: #70\n")
	got := backlog.ScanImported(root, "")
	want := []backlog.Imported{
		{Provider: "github", Repo: "web", Issue: 5, ItemID: "B01", Status: "open"}, {Provider: "github", Repo: "api", Issue: 5, ItemID: "B01", Status: "open"},
		{Provider: "gitlab", Repo: "web", Issue: 6, ItemID: "B01", Status: "open"}, {Provider: "gitlab", Repo: "api", Issue: 6, ItemID: "B01", Status: "open"},
		{Provider: "github", Repo: "", Issue: 7, ItemID: "B02", Status: "open"},
		{Provider: "gitlab", Repo: "", Issue: 9, ItemID: "F05", Status: "archived"},
		{Provider: "github", Repo: "", Issue: 70, ItemID: "B02", Status: "open"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries\n%+v\nwant\n%+v", got, want)
	}
	if got := backlog.ScanImported(root, "api"); len(got) != 2 || got[0].Issue != 5 {
		t.Errorf("for-repo: %+v", got)
	}
	if got := backlog.ScanImported(t.TempDir(), ""); len(got) != 0 {
		t.Errorf("empty project: %+v", got)
	}
}

const adoptBacklog = `# TODO

## Bugs
- **[B01] [P1] New bug.** Fresh. Related: [T01]

## Tasks
- **[T01] backlog.Imported task.** From the tracker. GH: #1 Related: [B01] Since: abc1234
`

func TestMigrateAdoptsTrackerImports(t *testing.T) {
	files := map[string]string{".rota/BACKLOG.md": adoptBacklog, ".rota/designs/T01.md": "design of B01\n"}
	// preview: adopt, not create; the would-be map points at the issue
	root := migProject(t, files)
	o, _, _ := newMig(t, root, false, nil)
	res, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, op := range res.Ops {
		got = append(got, op.Action+": "+op.Text)
	}
	for _, w := range []string{`adopt: T01 → #1 "backlog.Imported task" [type:task]`, `create-issue: B01 → bug "New bug" [type:bug, p1]`} {
		if !slices.Contains(got, w) {
			t.Errorf("preview ops %q lack %q", got, w)
		}
	}
	if e, _ := res.Map.Get("T01"); e == nil {
		t.Error("no map entry for T01")
	} else if n, _ := e.(*jsonx.Object).Get("number"); fmt.Sprint(n) != "1" {
		t.Errorf("would-be number %v", n)
	}

	// apply: no second issue for T01, labels added, design and Related still run
	root = migProject(t, files)
	f := &trackertest.MS{Fake: &trackertest.Fake{Issues: []tracker.Issue{{Number: 1, State: "open", Title: "backlog.Imported task", Body: "orig", URL: "u1"}}}}
	o, _, _ = newMig(t, root, true, f)
	res, err = Run(o)
	if err != nil || !res.Done || res.Migrated != 2 {
		t.Fatalf("apply: %v done %v migrated %d", err, res != nil && res.Done, res.Migrated)
	}
	if len(f.Issues) != 3 { // adopted #1, the milestone tracker M01, the new bug
		t.Fatalf("%d issues: %+v", len(f.Issues), f.Issues)
	}
	adopted := f.Issues[0]
	if !slices.Contains(adopted.Labels, "type:task") || !strings.HasPrefix(adopted.Body, "orig") || adopted.Title != "backlog.Imported task" {
		t.Errorf("adopted issue %+v", adopted)
	}
	if len(adopted.Comments) != 1 || !strings.Contains(adopted.Comments[0].Body, "design") {
		t.Errorf("design note %+v", adopted.Comments)
	}
	if !strings.Contains(adopted.Body, "Related: ") {
		t.Errorf("Related not set on the adopted issue: %q", adopted.Body)
	}
	if e, _ := res.Map.Get("T01"); e == nil {
		t.Fatal("no map entry")
	} else if id, _ := e.(*jsonx.Object).Get("id"); id != "T1" {
		t.Errorf("map id %v", id)
	}
	// a re-run adopts nothing twice
	calls := len(f.Calls)
	if res, err = Run(o); err != nil || res.Changed || len(f.Calls) != calls {
		t.Errorf("rerun: %v changed %v, %d new calls", err, res.Changed, len(f.Calls)-calls)
	}
}

// A slot and a queued PR that still hold file-mode IDs when the migration
// finishes are rewritten to the issue numbers (#27).
func TestMigrateRemapsRegistryIDs(t *testing.T) {
	root := migProject(t, map[string]string{".rota/workers.json": `{"slots":[` +
		`{"name":"ben","task":"B01","claimId":"ben@1","state":"busy"},` +
		`{"name":"dana","task":null,"state":"idle"}],` +
		`"prs":[{"issue":"#F01","pr":"#9"}]}`})
	f := &trackertest.MS{Fake: &trackertest.Fake{}}
	o, _, _ := newMig(t, root, true, f)
	res, err := Run(o)
	if err != nil || !res.Done {
		t.Fatalf("%v %+v", err, res)
	}
	reg := worker.LoadRegistry(root)
	if got := reg.Slot("ben").Task(); got != "2" {
		t.Errorf("task %q", got)
	}
	if got := reg.Slot("ben").ClaimID(); got != "ben@1" {
		t.Errorf("claimId %q", got)
	}
	if got := reg.PRs()[0].Issue; got != "4" {
		t.Errorf("queued issue %q", got)
	}
	// rerunning leaves the rewritten IDs alone
	if _, err := Run(o); err != nil {
		t.Fatal(err)
	}
	if got := worker.LoadRegistry(root).Slot("ben").Task(); got != "2" {
		t.Errorf("rerun task %q", got)
	}
}

// A preview must announce what the apply will do: the same Ops on one fixture.
// The ID a Related rewrite points at is the one thing a preview cannot know
// ("#?" before, the new ID after), so that target is masked on both sides.
func TestMigratePreviewAndApplyPlanTheSameOps(t *testing.T) {
	numRe := regexp.MustCompile(`(→ )(?:#\?|#?[A-Z]?\d+)$`)
	ops := func(apply bool) []string {
		root := migProject(t, nil)
		var f *trackertest.MS
		if apply {
			f = &trackertest.MS{Fake: &trackertest.Fake{}}
		}
		o, _, _ := newMig(t, root, apply, f)
		res, err := Run(o)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, op := range res.Ops {
			got = append(got, op.Action+": "+numRe.ReplaceAllString(op.Text, "${1}N"))
		}
		return got
	}
	preview, applied := ops(false), ops(true)
	if len(preview) == 0 {
		t.Fatal("the fixture planned no ops")
	}
	if !reflect.DeepEqual(preview, applied) {
		t.Errorf("preview ops:\n%q\napply ops:\n%q", preview, applied)
	}
}
