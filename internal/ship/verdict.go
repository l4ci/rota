package ship

import (
	"strings"

	"github.com/l4ci/rota/internal/verdict"
)

// VerdictBlockedError is a recorded FAIL that stops branch from shipping (B3).
// Stale is set when the branch has moved past the reviewed commit.
type VerdictBlockedError struct {
	Branch string
	Record verdict.Record
	Stale  bool
}

func (e *VerdictBlockedError) Error() string {
	return e.Record.Kind + " " + e.Record.Verdict + " recorded for " + e.Branch + "; not shipped"
}

// VerdictCheck reads the verdict store for one checkout.
type VerdictCheck struct {
	Git      Git
	Store    verdict.Store
	Repo     string // sub-repo name in an umbrella, else ""
	Settings verdict.Settings
}

// Block returns a *VerdictBlockedError when a recorded FAIL blocks branch, nil
// when nothing does.
func (v VerdictCheck) Block(branch string) error {
	r, ok := verdict.Blocking(v.Store.Branches[verdict.BranchKey(v.Repo, branch)], v.Settings)
	if !ok {
		return nil
	}
	// A branch whose tip cannot be read has moved on as far as we can tell.
	stale := true
	if res, err := v.Git.Run("rev-parse", "--short", branch); err == nil && res.ExitCode == 0 {
		stale = r.Sha != strings.TrimSpace(res.Stdout)
	}
	return &VerdictBlockedError{Branch: branch, Record: r, Stale: stale}
}
