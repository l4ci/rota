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
		"second-opinion PASS":                                {list: []Record{rec(SecondOpinion, Pass, head)}, head: head},
		"CONCERNS with minor finding":                        {list: []Record{{Kind: ReviewQuality, Verdict: Concerns, Sha: head, Findings: []Finding{{Severity: "minor", Title: "nonblocking"}}}}, head: head},
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

func TestBlockingGateResolving(t *testing.T) {
	const head = "bbbbbbb1234"
	stale := Record{Kind: ReviewCross, Verdict: Fail, Sha: "aaaaaaa", Harness: "hx", Model: "m"}
	boom := errors.New("cannot read depth")
	for name, tc := range map[string]struct {
		list    []Record
		off     func() (bool, error)
		blocked bool
		relaxed bool
		calls   int
		err     error
	}{
		"stale cross FAIL, cross on":  {list: []Record{stale}, off: func() (bool, error) { return false, nil }, blocked: true, calls: 1},
		"stale cross FAIL, cross off": {list: []Record{stale}, off: func() (bool, error) { return true, nil }, relaxed: true, calls: 1},
		"resolver error returned":     {list: []Record{stale}, off: func() (bool, error) { return false, boom }, blocked: true, calls: 1, err: boom},
		"nil resolver keeps block":    {list: []Record{stale}, blocked: true},
		"no stale cross, no lookup":   {list: nil, off: func() (bool, error) { return true, nil }},
		"FAIL at head, no lookup":     {list: []Record{{Kind: ReviewQuality, Verdict: Fail, Sha: head}}, off: func() (bool, error) { return true, nil }, blocked: true},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			var off func() (bool, error)
			if tc.off != nil {
				off = func() (bool, error) { calls++; return tc.off() }
			}
			d, err := BlockingGateResolving(tc.list, head, off)
			if !errors.Is(err, tc.err) || d.Blocked != tc.blocked || (d.Relaxed != nil) != tc.relaxed || calls != tc.calls {
				t.Fatalf("got %+v err %v calls %d; want blocked %v relaxed %v calls %d err %v", d, err, calls, tc.blocked, tc.relaxed, tc.calls, tc.err)
			}
		})
	}
}
