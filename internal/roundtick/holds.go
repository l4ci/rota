package roundtick

import (
	"context"
	"strings"

	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/strutil"
)

// mergeEligibility answers the per-target checks shared by merge and
// assign-ahead. It only reads state; merge owns reporting and hold bookkeeping.
// Caps and train grouping apply after these checks.
type mergeEligibility struct {
	head    string
	held    bool
	why     string
	blocked Item
}

func (a mergeEligibility) due() bool { return a.blocked.Kind == "" }

func (e Env) targetDue(ctx context.Context, failed map[string]FailedHead, target string) mergeEligibility {
	var a mergeEligibility
	if e.Head != nil {
		head, err := e.Head(ctx, target)
		if err != nil || head == "" {
			detail := "cannot read PR head; not retrying the gate"
			if err != nil {
				detail += ": " + strutil.FirstLine(err.Error())
			}
			a.blocked = Item{Kind: "gate-failed", Target: target, Why: detail}
			return a
		}
		a.head = head
	}
	a.why, a.held = HoldReason(e.Held, failed, target, a.head)
	if a.held {
		a.blocked = heldItem(target, a.why, e.Head != nil)
	} else if e.TrainFill.Enabled() && e.TrainReady != nil && !e.TrainReady(target) && (e.FillBypass == nil || !e.FillBypass(target)) {
		a.blocked = Item{Kind: "awaiting-review", Target: target, Why: "PR is not ready for train fill; waiting for current-head review readiness"}
	}
	return a
}

// HoldReason checks for a recorded hold on a target and its PR head.
// A new head clears a recorded failure; a failed head follows the PR when its
// slot changes. Legacy holds without a recorded head remain conservative.
func HoldReason(held map[string]string, failed map[string]FailedHead, target, head string) (string, bool) {
	if head == "" {
		why, ok := held[target]
		return why, ok
	}
	contentFailure := func(why string) bool {
		v, _, _ := strings.Cut(why, ":")
		return landing.ClassifyVerdict(v).HoldHead
	}
	if prev, ok := failed[target]; ok {
		if prev.Head == head && contentFailure(prev.Why) {
			return prev.Why, true
		}
	} else if why, ok := held[target]; ok && contentFailure(why) {
		return why, true
	}
	for _, prev := range failed {
		if prev.Head == head && contentFailure(prev.Why) {
			return prev.Why, true
		}
	}
	return "", false
}
