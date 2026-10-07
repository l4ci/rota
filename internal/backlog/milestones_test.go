package backlog

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
	ms "github.com/l4ci/rota/internal/milestone"
	"github.com/l4ci/rota/internal/tracker"
)

func TestParseMSTitle(t *testing.T) {
	for _, c := range []struct {
		in, id, rest string
		ok           bool
	}{
		{"M02 — Sharing", "M02", "Sharing", true},
		{"  M10 – Two words here ", "M10", "Two words here", true},
		{"M3 - dash", "M3", "dash", true},
		{"M04", "M04", "", true},
		{"M05x — nope", "", "", false},
		{"Milestone M02", "", "", false},
		{"", "", "", false},
	} {
		id, rest, ok := parseMSTitle(c.in)
		if id != c.id || rest != c.rest || ok != c.ok {
			t.Errorf("parseMSTitle(%q) = %q %q %v, want %q %q %v", c.in, id, rest, ok, c.id, c.rest, c.ok)
		}
	}
}

func msIssues(f *trackertest.MS) *Issues {
	return &Issues{Cfg: jsonx.NewObject(), Tracker: f}
}

func TestNextMilestoneIDAndAdd(t *testing.T) {
	f := &trackertest.MS{Fake: &trackertest.Fake{}, Native: []tracker.Milestone{{Number: 1, Title: "M01 — a"}, {Number: 2, Title: "M07 — b"}, {Number: 3, Title: "release 9"}}}
	b := msIssues(f)
	if id, err := b.NextMilestoneID(); err != nil || id != "M08" {
		t.Fatalf("next = %q %v", id, err)
	}
	id, err := b.MilestoneAdd("", "Title", "Sum  mary.", []string{"M01"}, "2026-10-02")
	if err != nil || id != "M08" {
		t.Fatalf("add = %q %v", id, err)
	}
	if len(f.Issues) != 1 || f.Issues[0].Title != "M08 — Title" || f.Issues[0].Milestone != "M08 — Title" {
		t.Fatalf("issue = %+v", f.Issues)
	}
	if f.Native[len(f.Native)-1].Description != "Sum mary." {
		t.Errorf("native description = %q", f.Native[len(f.Native)-1].Description)
	}
	if want := ms.StubOn("M08", "Title", "Sum  mary.", []string{"M01"}, "2026-10-02"); !strings.HasPrefix(f.Issues[0].Body, want) {
		t.Errorf("body does not start with the stub:\n%s", f.Issues[0].Body)
	}
}

// A gap in the numbering and a closed milestone both count toward the next ID.
func TestNextMilestoneIDCountsGapsAndClosed(t *testing.T) {
	f := &trackertest.MS{Fake: &trackertest.Fake{}}
	b := msIssues(f)
	if _, err := b.MilestoneAdd("M05", "gap", "", nil, "2026-10-02"); err != nil {
		t.Fatal(err)
	}
	if id, err := b.NextMilestoneID(); err != nil || id != "M06" {
		t.Fatalf("after M05 (gap below it): %q %v", id, err)
	}
	if _, err := b.MilestoneAdd("M09", "closed holder", "", nil, "2026-10-02"); err != nil {
		t.Fatal(err)
	}
	closed := "closed"
	if err := f.EditMilestone(nil, f.Native[len(f.Native)-1].Number, tracker.MilestoneEdit{State: &closed}); err != nil {
		t.Fatal(err)
	}
	if got := f.Native[len(f.Native)-1]; got.Title != "M09 — closed holder" || got.State != "closed" {
		t.Fatalf("native = %+v", got)
	}
	id, err := b.MilestoneAdd("", "Tenth", "Skips ahead", nil, "2026-10-02")
	if err != nil || id != "M10" {
		t.Fatalf("add after gap and closed max = %q %v", id, err)
	}
	if got := f.Native[len(f.Native)-1].Title; got != "M10 — Tenth" {
		t.Errorf("native title = %q", got)
	}
}

func TestMilestoneStatusNeedsMilestoneTracker(t *testing.T) {
	b := &Issues{Cfg: jsonx.NewObject(), Tracker: &trackertest.Fake{}} // a Fake alone has no native milestone calls
	if err := b.MilestoneStatus("M01", "active"); err == nil || !strings.Contains(err.Error(), "native milestone") {
		t.Errorf("err = %v", err)
	}
	f := &trackertest.MS{Fake: &trackertest.Fake{}}
	if err := msIssues(f).MilestoneStatus("M01", "bogus"); err == nil || !strings.Contains(err.Error(), "planned active shipped archived") {
		t.Errorf("bad status: %v", err)
	}
}

func TestDuplicateTrackingIssuesWarn(t *testing.T) {
	f := &trackertest.MS{Fake: &trackertest.Fake{Issues: []tracker.Issue{
		{Number: 9, Title: "M01 — late", Labels: []string{"milestone-tracker"}, State: "open"},
		{Number: 4, Title: "M01 — early", Labels: []string{"milestone-tracker"}, State: "open"},
		{Number: 2, Title: "M01 — closed", Labels: []string{"milestone-tracker"}, State: "closed"},
	}}}
	b := msIssues(f)
	var warns []string
	b.Warn = func(s string) { warns = append(warns, s) }
	is, err := b.TrackerIssue("M01")
	if err != nil || is.Number != 4 {
		t.Fatalf("tracker issue = %+v %v", is, err)
	}
	if want := []string{"3 tracking issues carry M01 (#2, #4, #9); using #4"}; !reflect.DeepEqual(warns, want) {
		t.Errorf("warns = %v", warns)
	}
	if _, err := b.TrackerIssue("M02"); err == nil {
		t.Error("unknown milestone found")
	}
}

func TestSlicePlansAcrossParts(t *testing.T) {
	f := &trackertest.MS{Fake: &trackertest.Fake{Issues: []tracker.Issue{
		{Number: 3, Title: "M02 — Sharing", Labels: []string{"milestone-tracker"}, State: "open"},
	}}}
	b := msIssues(f)
	b.NoteLimit = "82"
	long := strings.Repeat("line of plan text\n", 12)
	for _, u := range []string{"S10", "S02"} {
		if _, err := b.SlicePut("M02", u, long+u); err != nil {
			t.Fatal(err)
		}
	}
	units, err := b.SliceUnits("M02")
	if err != nil || !reflect.DeepEqual(units, []string{"S02", "S10"}) {
		t.Fatalf("units = %v %v", units, err)
	}
	plans, err := b.SlicePlans("")
	if err != nil || len(plans) != 2 || plans[0].Unit != "S02" || plans[1].Unit != "S10" {
		t.Fatalf("plans = %+v %v", plans, err)
	}
	if plans[0].Text != strings.TrimRight(long+"S02", "\n") {
		t.Errorf("multi-part note not reassembled: %q", plans[0].Text)
	}
	if text, ok, _ := b.SliceGet("M02", "S10"); !ok || text != long+"S10" {
		t.Errorf("SliceGet = %q %v", text, ok)
	}
	if rm, _ := b.SliceRm("M02", "S02"); !rm {
		t.Error("SliceRm of an existing note reported nothing removed")
	}
	if rm, _ := b.SliceRm("M02", "S02"); rm {
		t.Error("second SliceRm reported removal")
	}
	if p, _ := b.SlicePlans("M09"); len(p) != 0 {
		t.Errorf("plans of an unknown milestone: %v", p)
	}
}

// A tracking issue carries slice notes (plan:SNN) and a plain plan note side
// by side; each kind reads, upserts and removes independently.
func TestSliceNotesMixedWithPlainPlan(t *testing.T) {
	f := &trackertest.MS{Fake: &trackertest.Fake{Issues: []tracker.Issue{
		{Number: 3, Title: "M02 — Sharing", Labels: []string{"milestone-tracker"}, State: "open"},
	}}}
	b := msIssues(f)
	const n = "3"
	if _, ok, err := b.NoteGet(n, "plan:S01"); err != nil || ok {
		t.Fatalf("empty issue: ok=%v err=%v", ok, err)
	}
	if u, err := b.SliceUnits("M02"); err != nil || len(u) != 0 {
		t.Fatalf("units on an empty issue = %v %v", u, err)
	}
	for _, p := range []struct{ kind, text string }{
		{"plan:S01", "slice one\nline two"}, {"plan:S02", "slice two"}, {"plan", "plain plan note"},
	} {
		if ch, err := b.NotePut(n, p.kind, p.text); err != nil || !ch {
			t.Fatalf("put %s = %v %v", p.kind, ch, err)
		}
	}
	if got, ok, _ := b.NoteGet(n, "plan:S01"); !ok || got != "slice one\nline two" {
		t.Errorf("plan:S01 = %q %v", got, ok)
	}
	if got, ok, _ := b.NoteGet(n, "plan"); !ok || got != "plain plan note" {
		t.Errorf("plan = %q %v", got, ok)
	}
	if u, _ := b.SliceUnits("M02"); !reflect.DeepEqual(u, []string{"S01", "S02"}) {
		t.Errorf("units = %v (the plain plan note is not a unit)", u)
	}
	var marks []string
	for _, c := range f.Fake.Issues[0].Comments {
		marks = append(marks, strings.SplitN(c.Body, "\n", 2)[0])
	}
	if want := []string{"<!-- rota:plan:S01 -->", "<!-- rota:plan:S02 -->", "<!-- rota:plan -->"}; !reflect.DeepEqual(marks, want) {
		t.Errorf("markers = %v", marks)
	}
	if ch, err := b.NotePut(n, "plan:S01", "slice one\nline two"); err != nil || ch {
		t.Errorf("idempotent put = %v %v", ch, err)
	}
	if rm, err := b.NoteRm(n, "plan:S01"); err != nil || !rm {
		t.Fatalf("rm = %v %v", rm, err)
	}
	if rm, _ := b.NoteRm(n, "plan:S01"); rm {
		t.Error("second rm reported removal")
	}
	if u, _ := b.SliceUnits("M02"); !reflect.DeepEqual(u, []string{"S02"}) {
		t.Errorf("units after rm = %v", u)
	}
	if got, ok, _ := b.NoteGet(n, "plan"); !ok || got != "plain plan note" {
		t.Errorf("plain plan after slice rm = %q %v", got, ok)
	}
	b.NoteRm(n, "plan:S02")
	b.NoteRm(n, "plan")
	if len(f.Fake.Issues[0].Comments) != 0 {
		t.Errorf("comments left: %+v", f.Fake.Issues[0].Comments)
	}
	if _, err := b.NotePut(n, "plan:x1", "bad"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad slice kind: %v", err)
	}
}
