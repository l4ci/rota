package cli

import (
	"github.com/l4ci/rota/internal/verdict"
)

// lookupVerdicts preserves file mode and recovers only from a same-repository
// PR. Read failures leave the local verdict rules in force and are visible.
func lookupVerdicts(c *Ctx, t verdictBranch) []verdict.Record {
	cfg := c.deps().Config(t.root)
	if issue, err := issueBackend(c.deps(), t.root); err != nil || !issue {
		return verdict.Load(t.root).Branches[t.key]
	}
	f, err := c.deps().forge(c.Context(), cfg, "", t.dir)
	if err == nil {
		prs, e := f.OpenPRs(c.Context())
		err = e
		if err == nil {
			for _, pr := range prs {
				if pr.Branch == t.branch && pr.SameRepository {
					err = verdict.RecoverPR(c.Context(), t.root, t.key, t.head, pr.Number, f)
					break
				}
			}
		}
	}
	if err != nil {
		c.Warn("could not recover PR verdicts for %s: %v", t.branch, err)
	}
	return verdict.ForHead(verdict.Load(t.root).Branches[t.key], t.head)
}
