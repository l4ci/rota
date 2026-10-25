package worker

import (
	"context"
	"fmt"
	"slices"

	"github.com/l4ci/rota/internal/tracker"
)

// milestoneCloser is the tracker write capability needed when the forge does
// not auto-close issues because the PR landed on a milestone branch.
type milestoneCloser interface {
	AddLabels(context.Context, tracker.IssueKey, []string, bool) error
	Close(context.Context, tracker.IssueKey, string, string) error
}

// closeOnMilestone labels before closing: a label failure leaves the issue
// open and claimed for reconciliation. A landed PR still passes the gate,
// with a warning if either tracker write failed.
func (g *gate) closeOnMilestone(src tracker.ClosingSource, label string) bool {
	keys := g.bundleKeys()
	for _, n := range g.forge.ClosedNumbers(src.Body) {
		key := tracker.NumKey(n)
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	closed := true
	for _, key := range keys {
		if !g.issues.ClosesIssue(key, src) {
			continue
		}
		is, err := g.issues.Get(g.ctx, key, false)
		if err == nil && slices.Contains(is.Labels, PartialSliceLabel) && !g.o.FinalSlice {
			continue
		}
		if err == nil {
			mc, ok := g.issues.(milestoneCloser)
			if !ok {
				err = fmt.Errorf("issue tracker cannot label and close milestone issues")
			} else {
				if !slices.Contains(is.Labels, label) {
					err = mc.AddLabels(g.ctx, key, []string{label}, true)
				}
				if err == nil && is.State != "closed" {
					err = mc.Close(g.ctx, key, tracker.CloseCompleted, "Merged into "+g.o.Base+" via "+g.pr)
				}
			}
		}
		if err != nil {
			g.res.Warnings = append(g.res.Warnings, fmt.Sprintf("ISSUE-OPEN %s — %s merged but %s could not be labelled and closed on %s, so it keeps its claim for `rota round reconcile`: %v", g.o.Slot, g.pr, key.Ref(), g.o.Base, err))
			closed = false
		}
	}
	return closed
}
