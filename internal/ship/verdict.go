package ship

import (
	"strings"

	"github.com/l4ci/rota/internal/verdict"
)

// VerdictBlockedError is a recorded FAIL, or a review at the gate's head with a
// blocking finding, that stops branch from shipping (B3). Stale is set when the
// branch has moved past the reviewed commit. Finding is the title of the
// blocking finding when that is what blocks.
type VerdictBlockedError struct {
	Branch  string
	Record  verdict.Record
	Stale   bool
	Finding string
}

func (e *VerdictBlockedError) Error() string {
	if e.Finding != "" {
		return e.Record.Kind + " " + e.Record.Verdict + " recorded for " + e.Branch + " with a blocking finding (" + e.Finding + "); not shipped"
	}
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

// BlockGate is Block plus the bounce rule, for the merge gate and train: a
// review record at head (the commit the gate merges, which for a PR is the
// pushed head, not the local branch tip) with a blocking finding or a failed
// item refuses the merge even when its verdict is CONCERNS. Ship keeps Block,
// which follows the recorded FAIL alone.
func (v VerdictCheck) BlockGate(branch, head string) error {
	if err := v.Block(branch); err != nil {
		return err
	}
	r, ok := verdict.BlockingAtHead(v.Store.Branches[verdict.BranchKey(v.Repo, branch)], strings.TrimSpace(head))
	if !ok {
		return nil
	}
	e := &VerdictBlockedError{Branch: branch, Record: r}
	for _, f := range r.Findings {
		if f.IsBlocking() {
			e.Finding = f.Title
			break
		}
	}
	return e
}
