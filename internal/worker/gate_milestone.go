package worker

import (
	"context"
	"fmt"
	"slices"

	"github.com/l4ci/rota/internal/shlex"
	"github.com/l4ci/rota/internal/tracker"
)

// milestoneCloser is the tracker write capability needed when the forge does
// not auto-close issues because the PR landed on a milestone branch.
type milestoneCloser interface {
	AddLabels(context.Context, tracker.IssueKey, []string, bool) error
	Close(context.Context, tracker.IssueKey, string, string) error
}

// closeOnMilestone labels before closing: a label failure leaves the issue
// open and claimed until manually recovered. Each successful close releases
// its claim labels. A landed PR still passes with a warning for failed writes.
func (g *gateOperation) closeOnMilestone(src tracker.ClosingSource, label string) bool {
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
			add := "rota issues label " + shlex.Quote(string(key)) + " --add " + shlex.Quote(label)
			close := "rota issues close " + shlex.Quote(string(key)) + " --commit " + shlex.Quote(g.verified)
			g.res.Warnings = append(g.res.Warnings, fmt.Sprintf("ISSUE-OPEN %s — %s merged but %s could not be labelled and closed on %s, so it keeps its claim: %v; recover manually with `%s`, then `%s`", g.o.Slot, g.pr, key.Ref(), g.o.Base, err, add, close))
			closed = false
			continue
		}
		if n := releaseClaimLabel(g.ctx, releaseInput{Slot: g.o.Slot, Label: g.label, Issues: g.issues}, key); n != "" {
			g.res.Notes = append(g.res.Notes, n)
		}
	}
	return closed
}
