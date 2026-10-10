package cli

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// gateIssue is the issue a `worker gate` target stands for: the queued
// record's, else the one the slot holds. "" when it cannot be told.
func gateIssue(root, target string) string {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return "" // the gate itself refused a corrupt registry before this is asked
	}
	t, err := reg.GateTargetAny(target)
	if err != nil {
		return ""
	}
	if t.Queued {
		return t.Issue
	}
	return worker.HeldID(t.Task, t.Branch, t.Name)
}

// gateBounce does the per-item bounce accounting after a real gate run (never
// --check-only: that would count a peek). A pass forgets the item's count. A
// stale or provenance-fail verdict sends the PR back to its worker: it counts,
// and at round.maxBounces the item is parked needs-human instead (the PR stays
// open) so it is never re-dispatched silently. n is the item's count, parked
// whether it was parked; a failure to park is returned, the verdict stands.
func gateBounce(c *Ctx, root, issue string, r worker.GateResult) (n int, parked bool, err error) {
	if issue == "" {
		return 0, false, nil
	}
	switch r.Verdict {
	case worker.GatePass:
		// The PR lands: the item's wall-clock (work.itemTimeoutMinutes) ends too.
		if err := worker.ClearItemStart(root, issue); err != nil {
			return 0, false, err
		}
		if err := worker.ClearBestOf(root, issue); err != nil {
			return 0, false, err
		}
		return 0, false, worker.ClearBounces(root, issue)
	case worker.GateStale, worker.GateProvenanceFail:
	default:
		return 0, false, nil
	}
	if n, err = worker.RecordBounceIn(r.Round, root, issue, r.Slot, r.SHA); err != nil {
		return 0, false, err
	}
	set, err := roundcfg.Load(root)
	if err != nil || set.MaxBounces == 0 || n < set.MaxBounces {
		return n, false, err
	}
	env, be, err := moveEnv(c, root)
	if err != nil {
		return n, false, err
	}
	why, _, _ := strings.Cut(r.Err, "\n")
	what := r.PR
	if what == "" {
		what = "branch " + r.Branch
	}
	note := fmt.Sprintf("The merge gate sent %s back %d time(s) (round.maxBounces is %d), so it is parked for a human instead of going back to a worker. Last refusal: %s", what, n, set.MaxBounces, why)
	if _, err = env.Transfer(c.Context(), root, be, round.TransferOpts{
		Issue: issue, To: round.HumanTarget, Note: note, Settings: set,
	}); err != nil {
		return n, false, fmt.Errorf("could not park %s as needs-human: %w", issue, err)
	}
	return n, true, worker.ClearBounces(root, issue)
}
