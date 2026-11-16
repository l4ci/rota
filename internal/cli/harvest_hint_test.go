package cli

import (
	"context"
	"io"
	"strings"
	"testing"
)

// The gate and the train print this line after a landing, so the seam is the
// helper both call with the landed issues (AC-1, AC-2, AC-3, AC-4).
func TestHarvestHintLine(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	deps := trustedDeps(t, issueFixture(), harvestTrust{})
	c := &Ctx{Deps: deps, Stderr: io.Discard, ctx: context.Background()}

	// No learnings: no hint.
	if got := harvestHintLine(c, root, []string{"7"}); got != "" {
		t.Fatalf("no learnings, got hint %q", got)
	}

	body := bodyFile(t, "tier fold needs a clean tree\n")
	code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "item", "comment", "add", "7", "--kind", "learning", "--body-file", body)
	if code != 0 {
		t.Fatalf("comment add: exit %d: %s", code, stderr)
	}
	cid, _ := issueGet(issueData(t, env), "commentId").(string)

	got := harvestHintLine(c, root, []string{"#7", "7", "9"})
	if !strings.Contains(got, "#7 (1)") || !strings.Contains(got, "rota item comment list 7 --kind learning") || strings.Contains(got, "#9") {
		t.Fatalf("hint = %q", got)
	}

	// An issue that did not land is not named.
	if got := harvestHintLine(c, root, []string{"9"}); got != "" {
		t.Errorf("hint for an issue with no learning: %q", got)
	}

	// Once harvested, the hint goes away.
	harvestMerged(t, root, "7", 1)
	if code, _, stderr := rotaRunWith(t, deps, "--json", "-C", root, "knowledge", "harvest", "--mark", cid); code != 0 {
		t.Fatalf("mark: %d %s", code, stderr)
	}
	if got := harvestHintLine(c, root, []string{"7"}); got != "" {
		t.Errorf("harvested learning still hinted: %q", got)
	}
}

func TestHarvestHintLineFileBackendSilent(t *testing.T) {
	root := trackerProject(t, "")
	c := &Ctx{Deps: testDeps(), Stderr: io.Discard, ctx: context.Background()}
	if got := harvestHintLine(c, root, []string{"7"}); got != "" {
		t.Errorf("file backend hint %q", got)
	}
}
