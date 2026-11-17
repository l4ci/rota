package verdict

import "testing"

// Gate (check.go) and bounce must agree on whether a branch is failing when a
// recovered report (FromReport) sits beside a local verdict at the same head:
// the local verdict wins, the report does not count.
func TestGateAndBounceAgreeOnRecoveredReport(t *testing.T) {
	const head = "bbbbbbb"
	local := Record{Kind: ReviewSpec, Verdict: Pass, Combined: Pass, Sha: head}
	recovered := Record{Kind: ReviewSpec, Verdict: Fail, Combined: Fail, Sha: head, FromReport: true}
	for _, tc := range []struct {
		name string
		list []Record
		want bool
	}{
		{"local pass shadows recovered fail", []Record{local, recovered}, false},
		{"recovered fail alone counts", []Record{recovered}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, gate := BlockingAtHead(ForHead(tc.list, head), head)
			bounce := bounceReads(tc.list, head)
			if gate != tc.want || bounce != tc.want {
				t.Fatalf("gate=%v bounce=%v, want both %v", gate, bounce, tc.want)
			}
		})
	}
}

// bounceReads is the bounce path the cli takes: the review records at head,
// any of which is Failing. It does not call BounceFailing, so the gate rule
// is checked against an independent reading.
func bounceReads(list []Record, head string) bool {
	for _, r := range BounceReviewsAtHead(list, head) {
		if r.Failing() {
			return true
		}
	}
	return false
}

// Across the whole named-rule table the gate and the bounce path agree.
func TestGateAndBounceAgreeAcrossRuleCases(t *testing.T) {
	for _, c := range ruleCases() {
		_, gate := BlockingAtHead(ForHead(c.list, c.head), c.head)
		if bounce := bounceReads(c.list, c.head); gate != bounce {
			t.Errorf("%s: gate=%v bounce=%v", c.name, gate, bounce)
		}
	}
}
