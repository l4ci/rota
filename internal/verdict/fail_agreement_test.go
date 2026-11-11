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
			bounce := false
			for _, r := range BounceReviewsAtHead(tc.list, head) {
				bounce = bounce || r.Failing()
			}
			if gate != tc.want || bounce != tc.want {
				t.Fatalf("gate=%v bounce=%v, want both %v", gate, bounce, tc.want)
			}
		})
	}
}
