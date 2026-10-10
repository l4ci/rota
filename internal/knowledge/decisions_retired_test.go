package knowledge

import (
	"strings"
	"testing"
)

const retiredFixture = "# Decisions\n\nintro\n\n## Alpha\n\n### Live\n\nrule\n\n### Old\n\nrule\n\n*Status.* Retired 2026-10-10.\n\n## Beta\n\n### Gone\n\nrule\n\n*Status.* Superseded 2026-10-10 by \"New\".\n\n## Gamma\n\n### New\n\nrule\n"

func TestActiveDecisionsDropsRetiredEntriesAndEmptyTopics(t *testing.T) {
	got := ActiveDecisions(retiredFixture)
	for _, want := range []string{"## Alpha", "### Live", "## Gamma", "### New", "intro"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, bad := range []string{"### Old", "## Beta", "### Gone", "Status."} {
		if strings.Contains(got, bad) {
			t.Errorf("retired text %q survived:\n%s", bad, got)
		}
	}
}

func TestActiveDecisionsIdentityWithoutRetired(t *testing.T) {
	in := "# D\n\n## A\n\n### x\n\nrule\n"
	if got := ActiveDecisions(in); got != in {
		t.Fatalf("changed: %q", got)
	}
}
