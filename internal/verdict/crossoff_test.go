package verdict

import (
	"errors"
	"reflect"
	"testing"
)

// Both callers use BlockingGate. This table pins the gate's across-head rule
// and its stale-cross relaxation in one place, including the #3740 divergence.
func TestBlockingGate(t *testing.T) {
	for _, c := range ruleCases() {
		t.Run(c.name, func(t *testing.T) {
			got := BlockingGate(c.list, c.head, false)
			if want := c.blocking || c.atHead; got.Blocked != want {
				t.Fatalf("blocked = %v, want %v", got.Blocked, want)
			}
		})
	}
	const head = "bbbbbbb1234"
	rec := func(kind, v, sha string) Record {
		return Record{Kind: kind, Verdict: v, Sha: sha, Harness: "hx", Model: "m"}
	}
	finding := func(sha string) Record {
		r := rec(ReviewQuality, Concerns, sha)
		r.Findings = []Finding{{Severity: "major", Title: "unresolved"}}
		return r
	}
	staleFail := rec(ReviewCross, Fail, "aaaaaaa")
	for name, tc := range map[string]struct {
		list                                 []Record
		head                                 string
		off, blocked, relaxed, needsCrossOff bool
		stale                                bool
		finding                              string
	}{
		"no records":                                         {head: head},
		"FAIL at head, cross off":                            {list: []Record{rec(ReviewCross, Fail, head)}, head: head, off: true, blocked: true},
		"stale cross FAIL, cross off":                        {list: []Record{staleFail}, head: head, off: true, relaxed: true},
		"stale cross FAIL, cross required":                   {list: []Record{staleFail}, head: head, blocked: true, needsCrossOff: true, stale: true},
		"stale cross FAIL, unreadable head":                  {list: []Record{staleFail}, off: true, blocked: true, stale: true},
		"stale CONCERNS with blocking finding":               {list: []Record{finding("aaaaaaa")}, head: head},
		"current CONCERNS with blocking finding":             {list: []Record{finding(head)}, head: head, blocked: true, finding: "unresolved"},
		"stale cross CONCERNS, no lookup":                    {list: []Record{rec(ReviewCross, Concerns, "aaaaaaa")}, head: head},
		"another stale blocker still blocks":                 {list: []Record{staleFail, rec(ReviewSecurity, Fail, "aaaaaaa")}, head: head, off: true, blocked: true, stale: true},
		"current finding survives cross relaxation":          {list: []Record{staleFail, finding(head)}, head: head, off: true, blocked: true, finding: "unresolved"},
		"stale finding does not survive cross relaxation":    {list: []Record{staleFail, finding("aaaaaaa")}, head: head, off: true, relaxed: true},
		"current cross FAIL survives stale cross relaxation": {list: []Record{rec(ReviewCross, Fail, head), staleFail}, head: head, off: true, blocked: true},
		"stale report ignored":                               {list: []Record{{Kind: ReviewCross, Verdict: Fail, Sha: "aaaaaaa", FromReport: true}}, head: head},
		"current report still blocks":                        {list: []Record{{Kind: ReviewCross, Verdict: Fail, Sha: "bbbbbbb", FromReport: true}}, head: head, off: true, blocked: true},
		"newer review clears stale FAIL":                     {list: []Record{rec(ReviewSpec, Fail, "aaaaaaa"), rec(ReviewSpec, Pass, head)}, head: head},
		"newer cross clears finding":                         {list: []Record{{Kind: ReviewCross, Verdict: Concerns, Sha: head, Harness: "hx", Model: "m", Findings: []Finding{{Severity: "major", Title: "x"}}}, rec(ReviewCross, Pass, head)}, head: head},
	} {
		t.Run(name, func(t *testing.T) {
			before := append([]Record(nil), tc.list...)
			got := BlockingGate(tc.list, tc.head, tc.off)
			if got.Blocked != tc.blocked || (got.Relaxed != nil) != tc.relaxed || got.NeedsCrossOff != tc.needsCrossOff || got.Stale != tc.stale || got.Finding != tc.finding {
				t.Fatalf("decision = %+v, want blocked=%v relaxed=%v needsCrossOff=%v stale=%v finding=%q", got, tc.blocked, tc.relaxed, tc.needsCrossOff, tc.stale, tc.finding)
			}
			if got.Relaxed != nil && got.Relaxed.Sha != "aaaaaaa" {
				t.Fatalf("relaxed wrong record: %+v", got.Relaxed)
			}
			err := got.Err("branch")
			var blocked *BlockedError
			if errors.As(err, &blocked) != tc.blocked {
				t.Fatalf("error = %v, want blocked %v", err, tc.blocked)
			}
			if blocked != nil && (blocked.Branch != "branch" || blocked.Stale != tc.stale || blocked.Finding != tc.finding) {
				t.Fatalf("refusal changed: %+v", blocked)
			}
			if !reflect.DeepEqual(tc.list, before) {
				t.Fatal("decision mutated the records")
			}
		})
	}
}
