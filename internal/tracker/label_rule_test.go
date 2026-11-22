package tracker

import "testing"

// TestLabelRulePerForge is the one table for "is this the same label": every
// forge's case rule, through the matcher the adapters share.
func TestLabelRulePerForge(t *testing.T) {
	for _, tc := range []struct {
		forge    string
		rule     labelRule
		foldCase bool
	}{
		{"github", githubLabelRule, true},
		{"gitlab", gitlabLabelRule, false},
		{"linear", linearLabelRule, true},
		{"itsaplan", itsaplanLabelRule, true},
		{"jira", jiraLabelRule, false},
	} {
		t.Run(tc.forge, func(t *testing.T) {
			if sourceLabelRule(tc.forge) != tc.rule {
				t.Errorf("sourceLabelRule(%s) is not the forge's rule", tc.forge)
			}
			if got := sameLabelName(tc.rule, "Bug", "bug"); got != tc.foldCase {
				t.Errorf("sameLabelName(Bug, bug) = %v, want %v", got, tc.foldCase)
			}
			if !sameLabelName(tc.rule, "bug", "bug") || sameLabelName(tc.rule, "bug", "bugs") {
				t.Error("identical names must match and different names must not")
			}
			if got := labelKey(tc.rule, "Bug") == labelKey(tc.rule, "bug"); got != tc.foldCase {
				t.Errorf("labelKey equal for Bug/bug = %v, want %v", got, tc.foldCase)
			}
			if got := hasLabel(tc.rule, []string{"p1", "Bug"}, "bug"); got != tc.foldCase {
				t.Errorf("hasLabel = %v, want %v", got, tc.foldCase)
			}
			if hasLabel(tc.rule, []string{"p1"}, "bug") {
				t.Error("hasLabel matched an absent name")
			}
		})
	}
}
