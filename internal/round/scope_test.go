package round

import (
	"reflect"
	"testing"
)

func TestScopes(t *testing.T) {
	body := "## Goal\nx\n\n## Touches\n\n- POST /items \n* `Worker.Slot`\n- post /items\n\nprose line\n\n## Out of scope\n- other\n"
	got := Scopes(body)
	want := []string{"post /items", "worker.slot"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Scopes = %v want %v", got, want)
	}
	if got := Scopes("## Touches\n\n## Files\n- a.go\n"); len(got) != 0 {
		t.Errorf("empty section is none: %v", got)
	}
	if got := Scopes("no section"); len(got) != 0 {
		t.Errorf("no section is none: %v", got)
	}
}

func TestScopeOverlaps(t *testing.T) {
	got := ScopeOverlaps([]string{"post /items", "a"}, []string{"post /items", "post /items/{id}"})
	if !reflect.DeepEqual(got, []string{"post /items"}) {
		t.Errorf("got %v", got)
	}
	if got := ScopeOverlaps([]string{"post /items/{id}"}, []string{"post /items"}); len(got) != 0 {
		t.Errorf("different endpoints must not clash: %v", got)
	}
}

func scopeFixture() *fakeRemote {
	be := &fakeRemote{}
	be.add("1", "holder", "", false, "## Touches\n- POST /items\n- worker.Slot\n")
	be.add("2", "candidate", "", false, "## Touches\n- post /items \n")
	be.add("3", "other", "", false, "## Touches\n- POST /items/{id}\n")
	return be
}

func TestAssessScopeClash(t *testing.T) {
	be := scopeFixture()
	flight := []InFlight{{Slot: "ben", Issue: "1", Scopes: Scopes("## Touches\n- POST /items\n- worker.Slot\n")}}
	r, err := Assess(be, "2", tracked, nil, flight, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Overlaps) != 1 || !reflect.DeepEqual(r.Overlaps[0].Scopes, []string{"post /items"}) || r.Overlaps[0].With != "1" || r.Overlaps[0].Slot != "ben" {
		t.Fatalf("overlaps: %+v", r.Overlaps)
	}
	if !r.Ready() {
		t.Errorf("warn mode keeps the item ready: %+v", r.Checks)
	}
	if d := r.Checks[2].Detail; len(d) != 1 || d[0] != "1 (held by ben): scopes post /items" {
		t.Errorf("detail: %v", d)
	}
	r, _ = Assess(be, "3", tracked, nil, flight, false)
	if len(r.Overlaps) != 0 {
		t.Errorf("different endpoint: %+v", r.Overlaps)
	}
}

func TestScopeOverlapBlocks(t *testing.T) {
	be := scopeFixture()
	flight := []InFlight{{Slot: "ben", Issue: "1", Scopes: []string{"post /items"}}}
	r, _ := AssessScoped(be, "2", tracked, nil, flight, false, "", true)
	if r.Ready() || r.Checks[2].OK {
		t.Errorf("block mode must fail the overlap check: %+v", r.Checks)
	}
	r, _ = AssessScoped(be, "2", tracked, nil, flight, true, "", true)
	if !r.Ready() || len(r.Overlaps) != 1 {
		t.Errorf("--accept-overlap skips a scope clash and keeps it listed: %+v", r)
	}
}

func TestOverlapPathsAndScopesOneEntry(t *testing.T) {
	be := &fakeRemote{}
	be.add("7", "a", "", false, "changes internal/worker/pool.go")
	be.add("8", "b", "", false, "changes internal/worker/pool.go\n\n## Touches\n- x\n")
	flight := []InFlight{{Slot: "ben", Issue: "7", Paths: []string{"internal/worker/pool.go"}, Scopes: []string{"x"}}}
	r, _ := Assess(be, "8", tracked, nil, flight, false)
	if len(r.Overlaps) != 1 || len(r.Overlaps[0].Paths) != 1 || len(r.Overlaps[0].Scopes) != 1 {
		t.Fatalf("one entry with both lists: %+v", r.Overlaps)
	}
	if d := r.Checks[2].Detail; len(d) != 1 || d[0] != "7 (held by ben): internal/worker/pool.go; scopes x" {
		t.Errorf("detail: %v", d)
	}
	if r.Ready() {
		t.Error("a path clash still blocks")
	}
}

func TestAssessSubsystemFallback(t *testing.T) {
	be := &fakeRemote{}
	be.add("1", "a", "", false, "prose")
	be.add("2", "b", "", false, "prose")
	be.items["1"].Fields.Subsystem = "CLI"
	be.items["2"].Fields.Subsystem = "cli"
	flight := []InFlight{{Slot: "ben", Issue: "1", Scopes: itemScopes(be, "1")}}
	r, _ := Assess(be, "2", tracked, nil, flight, false)
	if len(r.Overlaps) != 1 || !reflect.DeepEqual(r.Overlaps[0].Scopes, []string{"subsystem:cli"}) {
		t.Errorf("overlaps: %+v", r.Overlaps)
	}
}

func TestAssessSubsystemIgnoredWhenTouches(t *testing.T) {
	be := &fakeRemote{}
	be.add("1", "a", "", false, "## Touches\n- POST /items\n")
	be.add("2", "b", "", false, "prose")
	be.items["1"].Fields.Subsystem = "cli"
	be.items["2"].Fields.Subsystem = "cli"
	flight := []InFlight{{Slot: "ben", Issue: "1", Scopes: itemScopes(be, "1")}}
	if r, _ := Assess(be, "2", tracked, nil, flight, false); len(r.Overlaps) != 0 {
		t.Errorf("a declared Touches side drops the subsystem fallback: %+v", r.Overlaps)
	}
	// An empty section counts as none.
	be.add("3", "c", "", false, "## Touches\n\n")
	be.items["3"].Fields.Subsystem = "cli"
	flight = []InFlight{{Slot: "ben", Issue: "3", Scopes: itemScopes(be, "3")}}
	if r, _ := Assess(be, "2", tracked, nil, flight, false); len(r.Overlaps) != 1 {
		t.Errorf("empty Touches keeps the fallback: %+v", r.Overlaps)
	}
}

func TestItemScopesUnknownItem(t *testing.T) {
	be := &fakeRemote{}
	if got := itemScopes(be, "404"); len(got) != 0 {
		t.Errorf("unknown item has no scopes: %v", got)
	}
}

func TestItemScopeSource(t *testing.T) {
	be := &fakeRemote{}
	be.add("1", "declared", "", false, "## Touches\n- a\n")
	be.add("2", "undeclared", "", false, "prose")
	if _, src := ItemScopeSource(be, "2"); src != SourceNone {
		t.Errorf("source = %q want none", src)
	}
	got, err := RecordInferred(be, "2", []string{"`Foo.Bar`", "go test ./x", "foo.bar"})
	if err != nil || !reflect.DeepEqual(got, []string{"foo.bar", "go test ./x"}) {
		t.Fatalf("RecordInferred = %v, %v", got, err)
	}
	if s, src := ItemScopeSource(be, "2"); src != SourceInferred || !reflect.DeepEqual(s, got) {
		t.Errorf("inferred: %v %q", s, src)
	}
	// A declared section wins over a recorded inferred scope, which is never edited away.
	if _, err := RecordInferred(be, "1", []string{"z"}); err != nil {
		t.Fatal(err)
	}
	if s, src := ItemScopeSource(be, "1"); src != SourceTouches || !reflect.DeepEqual(s, []string{"a"}) {
		t.Errorf("declared must win: %v %q", s, src)
	}
	if _, err := RecordInferred(be, "2", nil); err == nil {
		t.Error("no entries must be refused")
	}
	many := make([]string, MaxInferred+1)
	for i := range many {
		many[i] = string(rune('a' + i))
	}
	if _, err := RecordInferred(be, "2", many); err == nil {
		t.Error("more than the cap must be refused")
	}
}

func TestInferredScopeClashIsWarnOnly(t *testing.T) {
	be := scopeFixture()
	be.add("4", "no touches", "", false, "prose")
	if _, err := RecordInferred(be, "4", []string{"POST /items"}); err != nil {
		t.Fatal(err)
	}
	flight := []InFlight{{Slot: "ben", Issue: "1", Scopes: []string{"post /items"}, ScopeSource: SourceTouches}}
	r, err := AssessScoped(be, "4", tracked, nil, flight, false, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Ready() || len(r.Overlaps) != 1 || !r.Overlaps[0].Inferred {
		t.Fatalf("inferred clash must warn under block: %+v", r)
	}
	if d := r.Checks[2].Detail; len(d) != 1 || d[0] != "1 (held by ben): inferred scopes post /items" {
		t.Errorf("detail: %v", d)
	}
	// The holder's scope being inferred is warn-only too.
	flight = []InFlight{{Slot: "ben", Issue: "4", Scopes: []string{"post /items"}, ScopeSource: SourceInferred}}
	r, _ = AssessScoped(be, "2", tracked, nil, flight, false, "", true)
	if !r.Ready() || !r.Overlaps[0].Inferred {
		t.Errorf("holder inferred must warn: %+v", r)
	}
	// Two declared scopes still block.
	flight = []InFlight{{Slot: "ben", Issue: "1", Scopes: []string{"post /items"}, ScopeSource: SourceTouches}}
	if r, _ = AssessScoped(be, "2", tracked, nil, flight, false, "", true); r.Ready() {
		t.Errorf("declared clash must still block: %+v", r)
	}
}

// A Touches line that mimics a sentinel is hidden from the signed brief but
// still an overlap scope: issues in this repo name ROTA-DONE and friends as
// real identifiers, and two that do must still clash. Backtick-only bullets
// are no entry on either side.
func TestTouchEntriesHideSentinelsFromBriefOnly(t *testing.T) {
	body := "## Touches\n- POST /items\n* `Worker.Slot`\n- ROTA-DONE sentinel format\n- ``\n"
	if got := Scopes(body); !reflect.DeepEqual(got, []string{"post /items", "rota-done sentinel format", "worker.slot"}) {
		t.Errorf("Scopes = %v", got)
	}
	want := []touchEntry{
		{Raw: "POST /items", Norm: "post /items"},
		{Raw: "`Worker.Slot`", Norm: "worker.slot"},
		{Raw: "ROTA-DONE sentinel format", Norm: "rota-done sentinel format", Hidden: true},
	}
	if got := touchEntries(body); !reflect.DeepEqual(got, want) {
		t.Errorf("touchEntries = %+v want %+v", got, want)
	}
	be := &fakeRemote{details: map[string]string{"#9": body}}
	if got := touches(be, "#9"); got != "- POST /items\n- `Worker.Slot`" {
		t.Errorf("brief = %q", got)
	}
}
