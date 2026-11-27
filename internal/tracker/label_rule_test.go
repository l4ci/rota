package tracker

import (
	"testing"

	"github.com/l4ci/rota/internal/config"
)

// TestLabelRulePerForge is the one table for "is this the same label": every
// forge's case rule, through the matcher the adapters share.
func TestLabelRulePerForge(t *testing.T) {
	for _, tc := range []struct {
		forge    string
		rule     labelRule
		foldCase bool
		adapter  LabelSource
	}{
		{"github", githubLabelRule, true, &GitHub{}},
		{"gitlab", gitlabLabelRule, false, &GitLab{}},
		{"linear", linearLabelRule, true, &Linear{}},
		{"itsaplan", itsaplanLabelRule, true, &Itsaplan{}},
		{"jira", jiraLabelRule, false, &Jira{}},
	} {
		t.Run(tc.forge, func(t *testing.T) {
			if r, ok := sourceLabelRule(tc.forge); !ok || r != tc.rule {
				t.Errorf("sourceLabelRule(%s) is not the forge's rule", tc.forge)
			}
			if got := HasLabel(tc.adapter, []string{"Bug"}, "bug"); got != tc.foldCase {
				t.Errorf("HasLabel(Bug, bug) = %v, want %v", got, tc.foldCase)
			}
			if !HasLabel(tc.adapter, []string{"bug"}, "bug") || HasLabel(tc.adapter, []string{"bug"}, "bugs") {
				t.Error("identical names must match and different names must not")
			}
			if got := labelKey(tc.rule, "Bug") == labelKey(tc.rule, "bug"); got != tc.foldCase {
				t.Errorf("labelKey equal for Bug/bug = %v, want %v", got, tc.foldCase)
			}

		})
	}
}

// TestSourceLabelRuleCoversSources: every source an IssueActor can resolve is
// listed in sourceLabelRule, so none silently falls to exact matching.
func TestSourceLabelRuleCoversSources(t *testing.T) {
	sources := []string{config.SourceGitHub, config.SourceGitLab}
	for _, k := range OwnKinds() {
		sources = append(sources, k.Source)
	}
	for _, s := range sources {
		if _, ok := sourceLabelRule(s); !ok {
			t.Errorf("sourceLabelRule does not list source %q", s)
		}
	}
	// The adapters' own rules, as the table must agree with them.
	for s, want := range map[string]labelRule{
		config.SourceJira: jiraLabelRule, config.SourceLinear: linearLabelRule,
		config.SourceItsaplan: itsaplanLabelRule, config.SourceGitHub: githubLabelRule,
		config.SourceGitLab: gitlabLabelRule,
	} {
		if r, _ := sourceLabelRule(s); r != want {
			t.Errorf("sourceLabelRule(%s) = %v, want %v", s, r, want)
		}
	}
}
