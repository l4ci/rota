package tracker

import "testing"

// `gh issue view <n>` answers for a pull request number too, with state MERGED.
// A merged PR is finished work, so a dependency on it counts as done.
func TestGitHubNormStates(t *testing.T) {
	for _, tc := range []struct {
		state, want string
	}{
		{"OPEN", "open"},
		{"CLOSED", "closed"},
		{"MERGED", "closed"},
		{"merged", "closed"},
	} {
		if got := (ghIssue{Number: 534, State: tc.state}).norm().State; got != tc.want {
			t.Errorf("state %q normalised to %q, want %q", tc.state, got, tc.want)
		}
	}
}
