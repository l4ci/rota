package verdict

import "testing"

// The one block/not-block decision with the stale-cross relaxation (#3458),
// shared by the merge gate and the train tick (#3558).
func TestBlockingCrossOff(t *testing.T) {
	const head = "bbbbbbb1234"
	rec := func(kind, v, sha string) Record {
		return Record{Kind: kind, Verdict: v, Sha: sha, Harness: "hx", Model: "m"}
	}
	staleFail := rec(ReviewCross, Fail, "aaaaaaa")
	for name, tc := range map[string]struct {
		list    []Record
		head    string
		off     bool
		noFn    bool
		blocked bool
		relaxed bool
		asked   int
	}{
		"FAIL at head, cross off":            {list: []Record{rec(ReviewCross, Fail, head)}, head: head, off: true, blocked: true},
		"stale cross FAIL, cross off":        {list: []Record{staleFail}, head: head, off: true, relaxed: true, asked: 1},
		"stale cross FAIL, cross required":   {list: []Record{staleFail}, head: head, blocked: true, asked: 1},
		"stale cross FAIL, no cross lookup":  {list: []Record{staleFail}, head: head, noFn: true, blocked: true},
		"stale cross FAIL, unreadable head":  {list: []Record{staleFail}, head: "", off: true, blocked: true},
		"stale cross CONCERNS, no lookup":    {list: []Record{rec(ReviewCross, Concerns, "aaaaaaa")}, head: head},
		"another stale blocker still blocks": {list: []Record{staleFail, rec(ReviewSecurity, Fail, "aaaaaaa")}, head: head, off: true, blocked: true, asked: 1},
	} {
		t.Run(name, func(t *testing.T) {
			var asked int
			var off func() bool
			if !tc.noFn {
				off = func() bool { asked++; return tc.off }
			}
			_, blocked, relaxed := BlockingCrossOff(tc.list, tc.head, BlockingAnyHead, off)
			if blocked != tc.blocked {
				t.Errorf("blocked = %v, want %v", blocked, tc.blocked)
			}
			if (relaxed != nil) != tc.relaxed {
				t.Errorf("relaxed = %v, want %v", relaxed, tc.relaxed)
			}
			if relaxed != nil && relaxed.Sha != "aaaaaaa" {
				t.Errorf("relaxed record sha = %q, want the dropped stale FAIL", relaxed.Sha)
			}
			if asked != tc.asked {
				t.Errorf("crossOff asked %d times, want %d", asked, tc.asked)
			}
		})
	}
}

// The gate's side of the same decision: FAIL at head, stale cross FAIL with
// cross off, and stale cross FAIL with cross required.
func TestBlockGateCrossOff(t *testing.T) {
	const head = "bbbbbbb1234"
	spec := Record{Kind: ReviewSpec, Verdict: Pass, Sha: head}
	quality := Record{Kind: ReviewQuality, Verdict: Pass, Sha: head}
	cross := func(sha string) Record {
		return Record{Kind: ReviewCross, Verdict: Fail, Sha: sha, Harness: "hx", Model: "m"}
	}
	for name, tc := range map[string]struct {
		cross   Record
		off     bool
		blocked bool
		relaxed bool
		asked   int
	}{
		"FAIL at head, cross off":          {cross: cross(head), off: true, blocked: true},
		"stale cross FAIL, cross off":      {cross: cross("aaaaaaa"), off: true, relaxed: true, asked: 1},
		"stale cross FAIL, cross required": {cross: cross("aaaaaaa"), blocked: true, asked: 1},
	} {
		t.Run(name, func(t *testing.T) {
			var asked int
			check := Check{Store: Store{Branches: map[string][]Record{"b": {spec, quality, tc.cross}}}}
			relaxed, err := check.BlockGateCrossOff("b", head, func() bool { asked++; return tc.off })
			if (err != nil) != tc.blocked {
				t.Errorf("err = %v, want blocked %v", err, tc.blocked)
			}
			if (relaxed != nil) != tc.relaxed {
				t.Errorf("relaxed = %v, want %v", relaxed, tc.relaxed)
			}
			if asked != tc.asked {
				t.Errorf("crossOff asked %d times, want %d", asked, tc.asked)
			}
		})
	}
}
