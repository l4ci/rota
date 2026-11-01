package verdict

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/tracker"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseBodyAccepts(t *testing.T) {
	b, err := ParseBody(`{"summary": " s ", "findings": [{"severity": "major", "title": " t ", "file": "a.go", "line": 3, "detail": "d"}], "items": [{"id": "B07", "verdict": "PASS"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if b.Summary != "s" || len(b.Findings) != 1 || b.Findings[0].Severity != "major" || b.Findings[0].Title != "t" || b.Findings[0].File != "a.go" || b.Findings[0].Line != 3 || b.Findings[0].Detail != "d" || !b.Findings[0].IsBlocking() || b.Items[0] != (ItemVerdict{"B07", Pass}) {
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
	r := NewRecord(ReviewQuality, Pass, "s", b, recAt)
	if len(r.Declined) != 1 {
		t.Errorf("record dropped declined: %+v", r)
	}
	if o, _ := NewRecord(ReviewQuality, Pass, "s", Body{}, recAt).Object().Get("declined"); o != nil {
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

func rec(kind, v, sha string) Record { return NewRecord(kind, v, sha, Body{}, recAt) }

// TestCombinedPairing drives both writers of the review-quality Combined
// verdict (AddBranch and RecoverPR) over the same record sequences at one sha
// and expects the same answer, including with a cross or lens record between
// the spec and the quality record.
func TestCombinedPairing(t *testing.T) {
	const head = "abcdef1"
	cases := []struct {
		name string
		seq  []Record
		want string // Combined of the review-quality record, the last in seq
	}{
		{"quality only", []Record{rec(ReviewQuality, Concerns, head)}, Concerns},
		{"spec concerns + quality pass", []Record{rec(ReviewSpec, Concerns, head), rec(ReviewQuality, Pass, head)}, Concerns},
		{"spec pass + quality fail", []Record{rec(ReviewSpec, Pass, head), rec(ReviewQuality, Fail, head)}, Fail},
		{"cross between", []Record{rec(ReviewSpec, Concerns, head), rec(ReviewCross, Pass, head), rec(ReviewQuality, Pass, head)}, Concerns},
		{"lens between", []Record{rec(ReviewSpec, Fail, head), rec(ReviewSecurity, Pass, head), rec(ReviewQuality, Pass, head)}, Fail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			var got Record
			for _, r := range c.seq {
				out, err := AddBranch(root, "feat/x", r)
				if err != nil {
					t.Fatal(err)
				}
				got = out
			}
			if got.Combined != c.want {
				t.Errorf("AddBranch combined = %q, want %q", got.Combined, c.want)
			}
			var notes []tracker.Comment
			for _, r := range c.seq {
				notes = append(notes, tracker.Comment{Author: "me", Body: RenderReport(r, nil)})
			}
			rroot := t.TempDir()
			if err := RecoverPR(context.Background(), rroot, "feat/x", head, 1, &lookupSource{self: "me", notes: notes}); err != nil {
				t.Fatal(err)
			}
			q, ok := Latest(Load(rroot).Branches["feat/x"], ReviewQuality)
			if !ok || q.Combined != c.want {
				t.Errorf("RecoverPR combined = %q (found %v), want %q", q.Combined, ok, c.want)
			}
		})
	}
	t.Run("spec at another sha and non-review kinds", func(t *testing.T) {
		root := t.TempDir()
		add := func(r Record) Record {
			t.Helper()
			out, err := AddBranch(root, "feat/y", r)
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
		if r := add(rec(ReviewSpec, Concerns, "a0")); r.Combined != Concerns {
			t.Errorf("spec-only combined = %q", r.Combined)
		}
		add(rec(ReviewSpec, Fail, "a1"))
		if r := add(rec(ReviewQuality, Pass, "b2")); r.Combined != Pass {
			t.Errorf("cross-sha combined = %q", r.Combined)
		}
		if r := add(rec(QA, Fail, "b2")); r.Combined != "" {
			t.Errorf("qa got combined %q", r.Combined)
		}
		if got := len(Load(root).Branches["feat/y"]); got != 4 {
			t.Errorf("stored %d records", got)
		}
	})
}

func TestStoreCapsAndRoundTrips(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < keep+5; i++ {
		if _, err := AddBranch(root, "b", rec(QA, Pass, "s")); err != nil {
			t.Fatal(err)
		}
	}
	r := NewRecord(QA, Fail, "s", Body{Summary: "é <x>", Findings: []Finding{{Severity: "info", Title: "t", Line: 2}}}, recAt)
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

func TestWritersRefuseCorruptStore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o777); err != nil {
		t.Fatal(err)
	}
	writers := map[string]func() error{
		"AddBranch": func() error { _, err := AddBranch(root, "b", rec(QA, Pass, "a")); return err },
	}
	for _, text := range []string{"not json", `{"branches": 3}`, `[]`} {
		for name, write := range writers {
			if err := os.WriteFile(Path(root), []byte(text), 0o666); err != nil {
				t.Fatal(err)
			}
			err := write()
			var xe *exitcode.Error
			if !errors.As(err, &xe) || xe.Exit != exitcode.ExitRefused {
				t.Errorf("%s over %q: err %v, want exit %d", name, text, err, exitcode.ExitRefused)
			} else if !strings.Contains(xe.Message, Path(root)) || !strings.Contains(xe.Hint, "move it aside") {
				t.Errorf("%s over %q: message %q hint %q", name, text, xe.Message, xe.Hint)
			}
			if raw, _ := os.ReadFile(Path(root)); string(raw) != text {
				t.Errorf("%s over %q rewrote the file: %q", name, text, raw)
			}
		}
	}
}

func TestCanonicalizeItemsLeavesCorruptStore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o777); err != nil {
		t.Fatal(err)
	}
	const text = "not json"
	if err := os.WriteFile(Path(root), []byte(text), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := CanonicalizeItems(root, func(string) []string { return nil }); err != nil {
		t.Fatalf("err %v, want nil", err)
	}
	if raw, _ := os.ReadFile(Path(root)); string(raw) != text {
		t.Errorf("file rewritten: %q", raw)
	}
}

func TestWriterCreatesMissingStore(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := AddBranch(root, "b", rec(QA, Pass, "a")); err != nil {
		t.Fatal(err)
	}
	if n := len(Load(root).Branches["b"]); n != 1 {
		t.Errorf("recorded %d, want 1", n)
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
		{"review fail beats second opinion", []Record{rec(SecondOpinion, Fail, "a"), rec(ReviewSpec, Fail, "a")}, "", ReviewSpec},
		{"concerns", []Record{rec(ReviewQuality, Concerns, "a"), rec(SecondOpinion, Concerns, "a")}, "", ""},
		{"qa never", []Record{rec(QA, Fail, "a"), rec(QA, InfraFail, "a")}, "", ""},
		{"stale fail still blocks", []Record{rec(ReviewQuality, Fail, "old")}, "", ReviewQuality},
		{"newer pass clears", []Record{rec(ReviewQuality, Fail, "a"), rec(ReviewQuality, Pass, "b")}, "", ""},
		{"newer second opinion pass clears", []Record{rec(SecondOpinion, Fail, "a"), rec(SecondOpinion, Pass, "b")}, "", ""},
	}
	for _, c := range cases {
		r, ok := Blocking(c.list)
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

var recAt = time.Date(2026, 3, 4, 9, 5, 6, 0, time.UTC)

func TestNewRecordStampsTheGivenTime(t *testing.T) {
	if got := NewRecord(QA, Pass, "s", Body{}, recAt).RecordedAt; got != "2026-03-04T09:05:06Z" {
		t.Fatalf("RecordedAt = %q", got)
	}
}

// #899: the gate records a behaviour verdict, PASS or FAIL, with the path of
// the Try it transcript; verdict show lists it with the branch kinds.
func TestBehaviourKind(t *testing.T) {
	for v, want := range map[string]bool{Pass: true, Fail: true, Concerns: false, InfraFail: false} {
		if got, _ := Takes(Behaviour, v); got != want {
			t.Errorf("Takes(%s, %s) = %v", Behaviour, v, got)
		}
	}
	if !slices.Contains(BranchKinds, Behaviour) {
		t.Errorf("BranchKinds %v lacks %s", BranchKinds, Behaviour)
	}
	root := t.TempDir()
	r := rec(Behaviour, Fail, "abc1234")
	r.Transcript = ".rota/qa-runs/behaviour/b-abc1234.txt"
	if _, err := AddBranch(root, "b", r); err != nil {
		t.Fatal(err)
	}
	if got := Load(root).Branches["b"][0].Transcript; got != r.Transcript {
		t.Errorf("transcript round trip = %q", got)
	}
}

func TestFailingVersusEffectiveFail(t *testing.T) {
	blocker := []Finding{{Severity: "major", Title: "t"}}
	nit := []Finding{{Severity: "minor", Title: "t"}}
	cases := []struct {
		name        string
		r           Record
		failing     bool
		effectFails bool
	}{
		{"pass", Record{Kind: ReviewQuality, Verdict: Pass}, false, false},
		{"fail verdict", Record{Kind: ReviewQuality, Verdict: Fail}, true, true},
		{"concerns with nit", Record{Kind: ReviewQuality, Verdict: Concerns, Findings: nit}, false, false},
		{"concerns with blocker", Record{Kind: ReviewQuality, Verdict: Concerns, Findings: blocker}, true, false},
		{"pass with failed item", Record{Kind: ReviewSpec, Verdict: Pass, Items: []ItemVerdict{{"B07", Fail}}}, true, false},
	}
	for _, c := range cases {
		if got := c.r.Failing(); got != c.failing {
			t.Errorf("%s: Failing = %v, want %v", c.name, got, c.failing)
		}
		if _, got := EffectiveFail([]Record{c.r}); got != c.effectFails {
			t.Errorf("%s: EffectiveFail = %v, want %v", c.name, got, c.effectFails)
		}
	}
	// the combined verdict of a quality record decides, not its own Verdict
	q := Record{Kind: ReviewQuality, Verdict: Pass, Combined: Fail}
	if r, ok := EffectiveFail([]Record{q}); !ok || r.Verdict != Fail {
		t.Errorf("combined FAIL: %+v %v", r, ok)
	}
	if _, ok := EffectiveFail(nil); ok {
		t.Error("no records must not fail")
	}
}

// A review-cross FAIL blocks a ship like a review FAIL until a newer
// review-cross record clears it (#1612).
func TestBlockingReviewCross(t *testing.T) {
	list := []Record{{Kind: ReviewQuality, Verdict: Pass, Combined: Pass, Sha: "a"}, {Kind: ReviewCross, Verdict: Fail, Sha: "a", Harness: "codex"}}
	if r, ok := Blocking(list); !ok || r.Kind != ReviewCross {
		t.Fatalf("cross FAIL must block: %+v %v", r, ok)
	}
	list = append(list, Record{Kind: ReviewCross, Verdict: Pass, Sha: "b", Harness: "codex"})
	if r, ok := Blocking(list); ok {
		t.Fatalf("a newer cross PASS clears it: %+v", r)
	}
}

func TestParseBodyPatch(t *testing.T) {
	diff := "--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-x\n+y\n"
	in := `{"findings": [{"severity": "minor", "title": "t", "patch": ` + jsonString(diff) + `}, {"severity": "info", "title": "u"}]}`
	b, err := ParseBody(in)
	if err != nil {
		t.Fatal(err)
	}
	if b.Findings[0].Patch != diff {
		t.Errorf("patch = %q, want %q", b.Findings[0].Patch, diff)
	}
	if b.Findings[1].Patch != "" {
		t.Errorf("finding without patch got %q", b.Findings[1].Patch)
	}
	if _, err := ParseBody(`{"findings": [{"severity": "minor", "title": "t", "patch": 3}]}`); err == nil {
		t.Error("non-string patch accepted")
	}
}

func jsonString(s string) string {
	out, _ := json.Marshal(s)
	return string(out)
}

// A lens FAIL blocks a ship like a review FAIL until a newer record of the
// same lens clears it (#2064); a lens PASS never hides another lens's FAIL.
func TestBlockingLens(t *testing.T) {
	list := []Record{{Kind: ReviewQuality, Verdict: Pass, Combined: Pass, Sha: "a"}, {Kind: ReviewSecurity, Verdict: Fail, Sha: "a"}, {Kind: ReviewUX, Verdict: Pass, Sha: "a"}}
	if r, ok := Blocking(list); !ok || r.Kind != ReviewSecurity {
		t.Fatalf("Blocking = %v, %v; want the review-security FAIL", r, ok)
	}
	list = append(list, Record{Kind: ReviewSecurity, Verdict: Pass, Sha: "b"})
	if _, ok := Blocking(list); ok {
		t.Fatal("a newer review-security PASS must clear the block")
	}
}

func TestLensKinds(t *testing.T) {
	for _, name := range []string{"security", "perf", "ux"} {
		k := LensKind(name)
		if !IsLens(k) || !slices.Contains(BranchKinds, k) {
			t.Errorf("%s: IsLens=%v, in BranchKinds=%v", k, IsLens(k), slices.Contains(BranchKinds, k))
		}
		if !slices.Contains(kindVerdicts[k], Concerns) {
			t.Errorf("%s does not accept CONCERNS", k)
		}
	}
	if IsLens(ReviewCross) {
		t.Error("review-cross is not a lens")
	}
}

func TestWorkFixAttemptsAndRetention(t *testing.T) {
	root := t.TempDir()
	rec := func(kind, v string) Record { return NewRecord(kind, v, "abc1234", Body{}, time.Time{}) }
	for n := 1; n <= WorkFixCap; n++ {
		got, ok, err := AddWorkFix(root, "7", rec(WorkFix, Fail))
		if err != nil || !ok || got != n {
			t.Fatalf("attempt %d: got %d ok %v err %v", n, got, ok, err)
		}
	}
	if got, ok, _ := AddWorkFix(root, "7", rec(WorkFix, Fail)); ok || got != WorkFixCap {
		t.Fatalf("fourth attempt: got %d ok %v", got, ok)
	}
	// Active failures survive the history limit; work-fix records never count as debug fixes.
	for i := 0; i < keep+5; i++ {
		_, _ = AddItem(root, "7", rec(DebugFix, Pass))
	}
	list := Load(root).Items["7"]
	if got := WorkFixAttempts(list); got != WorkFixCap {
		t.Errorf("attempts after trimming = %d", got)
	}
	if FailedFixes(list) != 0 {
		t.Errorf("work-fix FAILs counted as debug fixes")
	}
	if got, ok, _ := AddWorkFix(root, "7", rec(WorkFix, Pass)); !ok || got != 0 {
		t.Errorf("pass: got %d ok %v", got, ok)
	}
}

// TestResetKindsAgreeAcrossCountersAndRetention checks that each reset record
// clears its counter and that retention then drops the cleared failures, so
// the counters and the history cut never disagree about what is still active.
func TestResetKindsAgreeAcrossCountersAndRetention(t *testing.T) {
	fail := func(k string) Record { return Record{Kind: k, Verdict: Fail} }
	cases := []struct {
		name  string
		fixk  string
		reset Record
		count func([]Record) int
	}{
		{"debug-reset", DebugFix, Record{Kind: DebugReset}, FailedFixes},
		{"work-fix-reset", WorkFix, Record{Kind: WorkFixReset}, WorkFixAttempts},
		{"work-fix-pass", WorkFix, Record{Kind: WorkFix, Verdict: Pass}, WorkFixAttempts},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var list []Record
			for i := 0; i < keep+5; i++ {
				list = appendItem(list, fail(c.fixk))
			}
			if got := c.count(list); got != keep+5 {
				t.Fatalf("active failures dropped before reset: counter %d, want %d", got, keep+5)
			}
			list = appendItem(list, c.reset)
			if got := c.count(list); got != 0 {
				t.Errorf("counter after reset = %d, want 0", got)
			}
			for i := 0; i < keep; i++ {
				list = appendItem(list, Record{Kind: "note"})
			}
			if len(list) != keep {
				t.Errorf("history after reset = %d records, want %d", len(list), keep)
			}
			if got := c.count(list); got != 0 {
				t.Errorf("counter after retention = %d, want 0", got)
			}
		})
	}
}

// BlockingAnyHead blocks on a FAIL, a blocking finding or a failed-item
// CONCERNS for every review kind, wherever the record was made, and never on
// a clean CONCERNS or PASS.
func TestBlockingAnyHead(t *testing.T) {
	blocker := []Finding{{Severity: "blocker", Title: "x"}}
	kinds := append([]string{ReviewCross, SecondOpinion}, LensKinds...)
	for _, kind := range kinds {
		cases := []struct {
			name string
			rec  Record
			want bool
		}{
			{"fail", Record{Kind: kind, Verdict: Fail, Sha: "old"}, true},
			{"blocking finding", Record{Kind: kind, Verdict: Concerns, Sha: "old", Findings: blocker}, kind != SecondOpinion},
			{"clean concerns", Record{Kind: kind, Verdict: Concerns, Sha: "old"}, false},
			{"pass", Record{Kind: kind, Verdict: Pass, Sha: "old"}, false},
		}
		for _, c := range cases {
			if _, got := BlockingAnyHead([]Record{c.rec}); got != c.want {
				t.Errorf("%s %s: blocked = %v, want %v", kind, c.name, got, c.want)
			}
		}
	}
	for _, c := range []struct {
		name string
		rec  Record
		want bool
	}{
		{"review fail", Record{Kind: ReviewQuality, Verdict: Fail, Combined: Fail, Sha: "old"}, true},
		{"review blocking finding", Record{Kind: ReviewQuality, Verdict: Concerns, Combined: Concerns, Sha: "old", Findings: blocker}, true},
		{"review clean concerns", Record{Kind: ReviewQuality, Verdict: Concerns, Combined: Concerns, Sha: "old"}, false},
	} {
		if _, got := BlockingAnyHead([]Record{c.rec}); got != c.want {
			t.Errorf("%s: blocked = %v, want %v", c.name, got, c.want)
		}
	}
}
