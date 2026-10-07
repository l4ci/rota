package acceptance

import (
	"reflect"
	"strings"
	"testing"
)

const legacy = "Intro.\n\n- [ ] outside list\n\n## Acceptance\n\n- [ ] first  thing\n- [x] done thing\n  * [ ] nested\n\n## Notes\n\n- [ ] not a criterion\n"

func TestNumberInOrderAndOnlyInSection(t *testing.T) {
	got, changed := Number(legacy)
	if !changed {
		t.Fatal("changed = false")
	}
	want := strings.Replace(legacy, "- [ ] first  thing", "- [ ] AC-1: first  thing", 1)
	want = strings.Replace(want, "- [x] done thing", "- [x] AC-2: done thing", 1)
	want = strings.Replace(want, "* [ ] nested", "* [ ] AC-3: nested", 1)
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	again, changed := Number(got)
	if changed || again != got {
		t.Fatal("second Number must be a no-op")
	}
}

func TestNumberKeepsIdsAndContinuesAfterMax(t *testing.T) {
	body := "## Acceptance\n- [ ] AC-1: a\n- [ ] new\n- [ ] AC-5: b\n- [ ] newer\n"
	got, _ := Number(body)
	want := "## Acceptance\n- [ ] AC-1: a\n- [ ] AC-6: new\n- [ ] AC-5: b\n- [ ] AC-7: newer\n"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestNumberNothingToDo(t *testing.T) {
	for _, b := range []string{"", "no section\n- [ ] x\n", "## Acceptance\n\nprose only\n"} {
		if got, changed := Number(b); changed || got != b {
			t.Errorf("%q: changed=%v got=%q", b, changed, got)
		}
	}
}

func TestParseTextAndDuplicates(t *testing.T) {
	crits, dups := Parse("## Acceptance\n- [ ] AC-1:  a   b\n- [ ] AC-1: c\n- [ ] plain\n")
	want := []Criterion{{"AC-1", "a b", 2}, {"AC-1", "c", 3}, {"", "plain", 4}}
	if !reflect.DeepEqual(crits, want) {
		t.Errorf("crits = %v", crits)
	}
	if !reflect.DeepEqual(dups, []string{"AC-1"}) {
		t.Errorf("dups = %v", dups)
	}
}

func TestIDsMatchesNumber(t *testing.T) {
	ids := IDs(legacy)
	if len(ids) != 3 || ids[0].ID != "AC-1" || ids[2].ID != "AC-3" || ids[1].Text != "done thing" {
		t.Fatalf("ids = %v", ids)
	}
}

func TestMarksRoundTrip(t *testing.T) {
	marks := []Mark{
		{"AC-10", "2026-10-07", "abc1234", "go test ./x", "ten"},
		{"AC-2", "2026-10-07", "abc1234", "go test ./a: b", "two · dots"},
	}
	note := RenderMarks(marks)
	want := "- AC-2 · 2026-10-07 · abc1234 · go test ./a: b · two · dots\n- AC-10 · 2026-10-07 · abc1234 · go test ./x · ten\n"
	if note != want {
		t.Fatalf("note = %q", note)
	}
	got := ParseMarks(note)
	if len(got) != 2 || got[0] != marks[1] || got[1] != marks[0] {
		t.Fatalf("parsed = %v", got)
	}
}

func TestUpsertReplaces(t *testing.T) {
	marks := []Mark{{"AC-1", "d", "aaaa111", "c", "t"}}
	marks = Upsert(marks, Mark{"AC-1", "d2", "bbbb222", "c", "t2"})
	marks = Upsert(marks, Mark{"AC-2", "d", "aaaa111", "c", "u"})
	if len(marks) != 2 || marks[0].Sha != "bbbb222" {
		t.Fatalf("marks = %v", marks)
	}
}

func TestFindProofLatestWinsAndShaPrefix(t *testing.T) {
	rows := []Row{
		{"go test ./x", "PASS", "abc1234"},
		{"go  test ./x", "FAIL", "abc1234"},
		{"other", "PASS", "abc1234"},
	}
	r, ok := FindProof(rows, "abc1234", "go test ./x")
	if !ok || r.Result != "FAIL" {
		t.Fatalf("latest must win: %v %v", r, ok)
	}
	if _, ok := FindProof(rows, "abc1234def", "other"); !ok {
		t.Error("long sha must match short row sha")
	}
	if _, ok := FindProof(rows, "abc", "other"); ok {
		t.Error("sha under 4 chars must not match")
	}
	if _, ok := FindProof(rows, "ffff000", "other"); ok {
		t.Error("different sha matched")
	}
}

func TestCoverage(t *testing.T) {
	body := "## Acceptance\n- [ ] AC-1: a\n- [ ] AC-2: b changed\n- [ ] AC-3: c\n- [ ] AC-4: d\n"
	marks := []Mark{
		{"AC-1", "d", "abc1234", "t1", "a"},
		{"AC-2", "d", "abc1234", "t1", "b"},
		{"AC-3", "d", "abc1234", "t2", "c"},
		{"AC-9", "d", "abc1234", "t1", "gone"},
	}
	rows := []Row{{"t1", "PASS", "abc1234"}, {"t2", "FAIL", "abc1234"}}
	got := Coverage(body, marks, rows)
	want := []Status{
		{"AC-1", "a", "abc1234:t1", "", true},
		{"AC-2", "b changed", "abc1234:t1", "changed", false},
		{"AC-3", "c", "abc1234:t2", "unproven", false},
		{"AC-4", "d", "", "", false},
		{"AC-9", "gone", "abc1234:t1", "missing", false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
}
