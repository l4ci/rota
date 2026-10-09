package verdict

import (
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBodyAccepts(t *testing.T) {
	b, err := ParseBody(`{"summary": " s ", "findings": [{"severity": "major", "title": " t ", "file": "a.go", "line": 3, "detail": "d"}], "items": [{"id": "B07", "verdict": "PASS"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if b.Summary != "s" || len(b.Findings) != 1 || b.Findings[0] != (Finding{"major", "t", "a.go", 3, "d"}) || b.Items[0] != (ItemVerdict{"B07", Pass}) {
		t.Errorf("got %+v", b)
	}
	if b, err := ParseBody(`{"verdict": "INFRA-FAIL"}`); err != nil || b.Verdict != InfraFail {
		t.Errorf("verdict in body: %+v %v", b, err)
	}
	if b, err := ParseBody("{}"); err != nil || b.Findings == nil || len(b.Findings) != 0 {
		t.Errorf("empty object: %+v %v", b, err)
	}
}

func TestParseBodyDeclined(t *testing.T) {
	b, err := ParseBody(`{"declined": [{"title": " t ", "file": "a.go", "line": 3, "detail": "needs a run"}]}`)
	if err != nil || len(b.Declined) != 1 || b.Declined[0] != (Declined{"t", "a.go", 3, "needs a run"}) {
		t.Errorf("got %+v %v", b, err)
	}
	for in, want := range map[string]string{
		`{"declined": [{"detail": "d"}]}`:                    "declined[0].title is required",
		`{"declined": [{"title": "t", "line": 0}]}`:          "declined[0].line must be 1 or more",
		`{"declined": [{"title": "t", "x": 1}]}`:             `unknown field "x"`,
		`{"declined": [{"title": "t", "severity": "info"}]}`: `unknown field "severity"`,
	} {
		if _, err := ParseBody(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", in, err, want)
		}
	}
	r := NewRecord(ReviewQuality, Pass, "s", b)
	if len(r.Declined) != 1 {
		t.Errorf("record dropped declined: %+v", r)
	}
	if o, _ := NewRecord(ReviewQuality, Pass, "s", Body{}).Object().Get("declined"); o != nil {
		t.Errorf("empty declined should be omitted, got %v", o)
	}
}

func TestParseBodyRejects(t *testing.T) {
	cases := map[string]string{
		"":                               "not a JSON object",
		"[1]":                            "not a JSON object",
		"null":                           "not a JSON object",
		"{":                              "invalid verdict body",
		`{} {}`:                          "text after the JSON object",
		`{"verdict": "pass"}`:            "verdict must be PASS",
		`{"finding": []}`:                `unknown field "finding"`,
		`{"summary": 3}`:                 "summary must be a string",
		`{"findings": {}}`:               "findings must be a list",
		`{"findings": [{"title": "t"}]}`: "findings[0].severity",
		`{"findings": [{"severity": "high", "title": "t"}]}`:              "findings[0].severity",
		`{"findings": [{"severity": "info"}]}`:                            "findings[0].title is required",
		`{"findings": [{"severity": "info", "title": " "}]}`:              "findings[0].title is required",
		`{"findings": [{"severity": "info", "title": "t", "line": 0}]}`:   "findings[0].line must be 1 or more",
		`{"findings": [{"severity": "info", "title": "t", "line": 1.5}]}`: "line must be a whole number",
		`{"findings": [{"severity": "info", "title": "t", "x": 1}]}`:      `unknown field "x"`,
		`{"items": [{"verdict": "PASS"}]}`:                                "items[0].id is required",
		`{"items": [{"id": "B1", "verdict": "INFRA-FAIL"}]}`:              "items[0].verdict",
	}
	for in, want := range cases {
		_, err := ParseBody(in)
		ae, ok := err.(*exitcode.Error)
		if !ok || ae.Exit != exitcode.ExitUsage || !strings.Contains(ae.Message, want) {
			t.Errorf("%q: got %v, want exit 2 containing %q", in, err, want)
		}
	}
}

func TestTakes(t *testing.T) {
	for kind, ok := range map[string]map[string]bool{
		ReviewSpec:    {Pass: true, Concerns: true, Fail: true, InfraFail: false},
		SecondOpinion: {Pass: true, Concerns: true, Fail: true, InfraFail: false},
		QA:            {Pass: true, Concerns: true, Fail: true, InfraFail: true},
		DebugFix:      {Pass: true, Concerns: false, Fail: true, InfraFail: false},
	} {
		for v, want := range ok {
			if got, _ := Takes(kind, v); got != want {
				t.Errorf("Takes(%s, %s) = %v", kind, v, got)
			}
		}
	}
	if got, _ := Takes(QA, "pass"); got {
		t.Error("verdicts are case-sensitive")
	}
}

func TestWorst(t *testing.T) {
	order := []string{Pass, Concerns, Fail, InfraFail}
	for i, a := range order {
		for j, b := range order {
			want := order[max(i, j)]
			if got := Worst(a, b); got != want {
				t.Errorf("Worst(%s, %s) = %s, want %s", a, b, got, want)
			}
		}
	}
}

func rec(kind, v, sha string) Record { return NewRecord(kind, v, sha, Body{}) }

func TestAddBranchCombines(t *testing.T) {
	root := t.TempDir()
	add := func(r Record) Record {
		t.Helper()
		out, err := AddBranch(root, "feat/x", r)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	add(rec(ReviewSpec, Concerns, "a1"))
	if r := add(rec(ReviewQuality, Pass, "a1")); r.Combined != Concerns {
		t.Errorf("spec CONCERNS + quality PASS combined = %q", r.Combined)
	}
	// A quality record after a spec at another sha does not combine.
	add(rec(ReviewSpec, Fail, "a1"))
	if r := add(rec(ReviewQuality, Pass, "b2")); r.Combined != Pass {
		t.Errorf("cross-sha combined = %q", r.Combined)
	}
	// Quality-only review: combined is its own verdict.
	if r := add(rec(ReviewQuality, Concerns, "b2")); r.Combined != Concerns {
		t.Errorf("quality-only combined = %q", r.Combined)
	}
	if r := add(rec(QA, Fail, "b2")); r.Combined != "" {
		t.Errorf("qa got combined %q", r.Combined)
	}
	if got := len(Load(root).Branches["feat/x"]); got != 6 {
		t.Errorf("stored %d records", got)
	}
}

func TestStoreCapsAndRoundTrips(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < keep+5; i++ {
		if _, err := AddBranch(root, "b", rec(QA, Pass, "s")); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRecord(QA, Fail, "s", Body{Summary: "é <x>", Findings: []Finding{{Severity: "info", Title: "t", Line: 2}}})
	if _, err := AddBranch(root, "b", r); err != nil {
		t.Fatal(err)
	}
	list := Load(root).Branches["b"]
	if len(list) != keep {
		t.Fatalf("kept %d, want %d", len(list), keep)
	}
	last := list[len(list)-1]
	if last.Summary != "é <x>" || last.Findings[0].Line != 2 {
		t.Errorf("round trip: %+v", last)
	}
	raw, _ := os.ReadFile(Path(root))
	if !strings.Contains(string(raw), `"\u00e9 <x>"`) || !strings.HasSuffix(string(raw), "}\n") {
		t.Errorf("file not in the conventions' JSON form:\n%s", raw)
	}
}

func TestLoadToleratesCorruptStore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o777); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"not json", `{"branches": 3}`, `[]`} {
		if err := os.WriteFile(Path(root), []byte(text), 0o666); err != nil {
			t.Fatal(err)
		}
		s := Load(root)
		if s.Branches == nil || s.Items == nil || len(s.Branches) != 0 {
			t.Errorf("%q: %+v", text, s)
		}
	}
}

func TestEffectiveReview(t *testing.T) {
	q := rec(ReviewQuality, Pass, "a")
	q.Combined = Concerns
	cases := []struct {
		name string
		list []Record
		want string
		ok   bool
	}{
		{"none", []Record{rec(QA, Fail, "a")}, "", false},
		{"quality uses combined", []Record{rec(ReviewSpec, Concerns, "a"), q, rec(QA, Fail, "a")}, Concerns, true},
		{"newer spec wins", []Record{q, rec(ReviewSpec, Fail, "a")}, Fail, true},
		{"spec only", []Record{rec(ReviewSpec, Pass, "a")}, Pass, true},
		{"second opinion ignored", []Record{rec(ReviewSpec, Pass, "a"), rec(SecondOpinion, Fail, "a")}, Pass, true},
	}
	for _, c := range cases {
		r, ok := EffectiveReview(c.list)
		if ok != c.ok || r.Verdict != c.want {
			t.Errorf("%s: got %q %v", c.name, r.Verdict, ok)
		}
	}
}

func TestAddItemCountsFailedFixes(t *testing.T) {
	root := t.TempDir()
	want := []int{1, 1, 2, 3}
	for i, v := range []string{Fail, Pass, Fail, Fail} {
		n, err := AddItem(root, "B07", rec(DebugFix, v, "s"))
		if err != nil {
			t.Fatal(err)
		}
		if n != want[i] {
			t.Errorf("after %d records: %d failed, want %d", i+1, n, want[i])
		}
	}
	if n, _ := AddItem(root, "B08", rec(DebugFix, Fail, "s")); n != 1 {
		t.Errorf("items share a count: %d", n)
	}
}

func TestFailedFixesStartsAfterReset(t *testing.T) {
	list := []Record{rec(DebugFix, Fail, "a"), rec(DebugFix, Fail, "a"), rec(DebugReset, Reset, "a"), rec(DebugFix, Pass, "b"), rec(DebugFix, Fail, "b")}
	if n := FailedFixes(list); n != 1 {
		t.Errorf("after reset: %d, want 1", n)
	}
	if n := FailedFixes(list[:3]); n != 0 {
		t.Errorf("reset last: %d, want 0", n)
	}
	if n := FailedFixes(list[:2]); n != 2 {
		t.Errorf("before reset: %d, want 2", n)
	}
	if KnownKind(DebugReset) {
		t.Error("debug-reset must not be an accepted kind")
	}
}

func TestResetItem(t *testing.T) {
	root := t.TempDir()
	if n, err := ResetItem(root, "B07", rec(DebugReset, Reset, "s")); err != nil || n != 0 {
		t.Fatalf("empty item: %d %v", n, err)
	}
	if len(Load(root).Items["B07"]) != 0 {
		t.Error("reset with nothing to clear wrote a record")
	}
	for i := 0; i < 3; i++ {
		AddItem(root, "B07", rec(DebugFix, Fail, "s"))
	}
	if n, err := ResetItem(root, "B07", rec(DebugReset, Reset, "s")); err != nil || n != 3 {
		t.Fatalf("cleared %d %v, want 3", n, err)
	}
	list := Load(root).Items["B07"]
	if len(list) != 4 || FailedFixes(list) != 0 {
		t.Errorf("after reset: %d records, %d failed", len(list), FailedFixes(list))
	}
	if n, _ := ResetItem(root, "B07", rec(DebugReset, Reset, "s")); n != 0 || len(Load(root).Items["B07"]) != 4 {
		t.Error("second reset must not append")
	}
}

func TestBlocking(t *testing.T) {
	q := rec(ReviewQuality, Pass, "a")
	cases := []struct {
		name   string
		list   []Record
		runner string
		kind   string
	}{
		{"none", nil, "", ""},
		{"review fail", []Record{rec(ReviewQuality, Fail, "a")}, "", ReviewQuality},
		{"spec fail newer than quality pass", []Record{q, rec(ReviewSpec, Fail, "a")}, "", ReviewSpec},
		{"quality pass newer than spec fail, other sha", []Record{rec(ReviewSpec, Fail, "a"), q}, "", ""},
		{"second opinion fail", []Record{rec(SecondOpinion, Fail, "a")}, "", SecondOpinion},
		{"second opinion fail, codex advisory", []Record{rec(SecondOpinion, Fail, "a")}, "codex", ""},
		{"review fail beats second opinion", []Record{rec(SecondOpinion, Fail, "a"), rec(ReviewSpec, Fail, "a")}, "", ReviewSpec},
		{"review fail blocks under codex", []Record{rec(ReviewSpec, Fail, "a")}, "codex", ReviewSpec},
		{"concerns", []Record{rec(ReviewQuality, Concerns, "a"), rec(SecondOpinion, Concerns, "a")}, "", ""},
		{"qa never", []Record{rec(QA, Fail, "a"), rec(QA, InfraFail, "a")}, "", ""},
		{"stale fail still blocks", []Record{rec(ReviewQuality, Fail, "old")}, "", ReviewQuality},
		{"newer pass clears", []Record{rec(ReviewQuality, Fail, "a"), rec(ReviewQuality, Pass, "b")}, "", ""},
		{"newer second opinion pass clears", []Record{rec(SecondOpinion, Fail, "a"), rec(SecondOpinion, Pass, "b")}, "", ""},
	}
	for _, c := range cases {
		r, ok := Blocking(c.list, Settings{Runner: c.runner})
		if ok != (c.kind != "") || r.Kind != c.kind {
			t.Errorf("%s: got %q %v, want %q", c.name, r.Kind, ok, c.kind)
		}
	}
}

func TestAtHead(t *testing.T) {
	full := "bbbbbbb0123456789012345678901234567890a"
	for _, c := range []struct {
		name, rec, head string
		want            bool
	}{
		{"short record, full head", "bbbbbbb", full, true},
		{"full record, short head", full, "bbbbbbb", true},
		{"equal", "bbbbbbb", "bbbbbbb", true},
		{"other commit", "aaaaaaa", full, false},
		{"empty record sha", "", full, false},
		{"empty head", "bbbbbbb", "", false},
		{"both empty", "", "", false},
	} {
		if got := AtHead(Record{Sha: c.rec}, c.head); got != c.want {
			t.Errorf("%s: AtHead(%q, %q) = %v, want %v", c.name, c.rec, c.head, got, c.want)
		}
	}
}
