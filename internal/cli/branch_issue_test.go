package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// On a key-based tracker, review scope and the follow-up guard read the same
// leading issue from the branch as review depth does, with or without a slug.
func TestTrackerKeyBranchScopeAndFollowUpGuard(t *testing.T) {
	for _, branch := range []string{"kit/ENG-1-fix", "kit/ENG-1"} {
		t.Run(branch, func(t *testing.T) {
			dir := verdictRepo(t)
			_, conn, it := linearSource(t, dir)
			deps := testDeps()
			deps.TrackerOptions = append(deps.TrackerOptions, conn)
			write(t, filepath.Join(dir, ".rota", "config.json"), linearCfg)
			key, err := it.Create(context.Background(), "Review follow-ups from PR #70", "## Acceptance\n- [ ] AC-1: x\n", nil, "")
			if err != nil || key != "ENG-1" {
				t.Fatal(key, err)
			}
			gittest.Run(t, dir, "checkout", "-q", "-b", branch)

			code, out, errOut := rotaInWith(t, deps, dir, "review", "scope", "--json")
			if code != 0 {
				t.Fatalf("scope: %d %s %s", code, out, errOut)
			}
			if intents, _ := data(t, out)["intents"].([]any); len(intents) != 1 {
				t.Errorf("intents = %v, want ENG-1; ids %v", data(t, out)["intents"], data(t, out)["referencedIds"])
			}

			body := writeBody(t, t.TempDir(), `{"findings":[{"severity":"major","title":"big","blocking":false}]}`)
			code, out, errOut = rotaInWith(t, deps, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77", "--json")
			if !strings.Contains(out, "filedWhy") || !strings.Contains(out, "follow-up") {
				t.Errorf("verdict add filed a follow-up of a follow-up (exit %d):\n%s\n%s", code, out, errOut)
			}
		})
	}
}
