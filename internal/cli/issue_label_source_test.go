package cli

import (
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// Rounds combine PRs from one forge with issues from an independent tracker.
func TestCombinedSidesLabelRuleUsesIssueTracker(t *testing.T) {
	for _, tc := range []struct {
		forge, issues string
		found         bool
	}{
		{"github", "jira", false}, {"gitlab", "linear", true},
	} {
		t.Run(tc.forge+"/"+tc.issues, func(t *testing.T) {
			sides := bothSides{Forge: labelSourceForge{source: tc.forge}, IssueTracker: labelSourceIssues{source: tc.issues}}
			if got := tracker.HasLabel(sides, []string{"Partial-Slice"}, "partial-slice"); got != tc.found {
				t.Fatalf("issue label found=%v, want %v for %s issues on %s forge", got, tc.found, tc.issues, tc.forge)
			}
		})
	}
}

type labelSourceForge struct {
	tracker.Forge
	source string
}

func (f labelSourceForge) Provider() string { return f.source }

type labelSourceIssues struct {
	tracker.IssueTracker
	source string
}

func (f labelSourceIssues) Provider() string { return f.source }

func (f labelSourceIssues) LabelMatcher() tracker.LabelMatcher {
	return (tracker.IssueActor{Source: f.source}).LabelMatcher()
}
