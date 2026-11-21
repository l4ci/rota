package release

import "testing"

func TestPendingState(t *testing.T) {
	const day = 86400
	cases := []struct {
		name    string
		tag     string
		commits int
		tagTS   int64
		want    PendingState
	}{
		{"no tag", "", 5, 0, PendingState{ThresholdCommits: int64(10), ThresholdDays: int64(14), Reason: "no-tag"}},
		{"commits due", "v1", 10, 0, PendingState{LastTag: "v1", Commits: 10, Days: 20, ThresholdCommits: int64(10), ThresholdDays: int64(14),
			ShouldNudge: true, Reason: "commits", Message: "10 commits since v1; consider /rota-release."}},
		{"days due", "v1", 2, 0, PendingState{LastTag: "v1", Commits: 2, Days: 20, ThresholdCommits: int64(10), ThresholdDays: int64(14),
			ShouldNudge: true, Reason: "days", Message: "2 commits and 20 days since v1; consider /rota-release."}},
		{"nothing since tag", "v1", 0, 0, PendingState{LastTag: "v1", Days: 20, ThresholdCommits: int64(10), ThresholdDays: int64(14)}},
	}
	for _, tc := range cases {
		if got := Pending(nil, tc.tag, tc.commits, tc.tagTS, 20*day); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
