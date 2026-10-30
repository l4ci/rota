package roundtick

import (
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// ReadinessInput is everything the merge-readiness rule reads for one PR: the
// branch head, the autopilot state, the bounce record and the branch's
// recorded verdicts. The caller resolves them; the rule lives in ReadinessOf.
type ReadinessInput struct {
	// Head is the branch's current commit; "" when it cannot be resolved.
	Head string
	// PR is the PR ref and Names the registry names it is held under.
	PR    string
	Names []string
	State State
	// BounceHead is the head the item was last sent back at, "" for none.
	BounceHead string
	// ReviewOff is a fill-enabled round whose review depth is none.
	ReviewOff bool
	// DoneAt is when the slot finished; it dates readiness under ReviewOff.
	DoneAt time.Time
	// Records are the branch's recorded verdicts.
	Records []verdict.Record
}

// Readiness is the verdict of the rule: Ready when the PR may join a train,
// Since the current-head review time (zero when unknown), Immediate when it
// is ready with no usable time and must skip the fill wait.
type Readiness struct {
	Ready     bool
	Since     time.Time
	Immediate bool
}

// ReadinessOf is the one merge-readiness rule. The tick reads it through
// Waiting and the train nudge through the same call, so advice cannot call a
// PR ready while the tick disagrees. A PR is ready when no hold or bounce
// stands on its head, no recorded verdict blocks it, and (unless review is
// off) a review is recorded at the head.
func ReadinessOf(in ReadinessInput) Readiness {
	if in.Head == "" {
		return Readiness{}
	}
	if n, ok := worker.PRRefNumber(in.PR); ok {
		names := append(append([]string{}, in.Names...), in.PR, fmt.Sprintf("#%d", n), fmt.Sprint(n))
		for _, name := range names {
			if _, held := HoldReason(in.State.Held, in.State.FailedHeads, name, fmt.Sprintf("%d:%s", n, in.Head)); held {
				return Readiness{}
			}
		}
	}
	if bh := in.BounceHead; bh != "" && (strings.HasPrefix(in.Head, bh) || strings.HasPrefix(bh, in.Head)) {
		return Readiness{} // sent back and not yet pushed to
	}
	blocked := TrainBlocked(in.Records)
	if in.ReviewOff {
		ready := !blocked
		return Readiness{Ready: ready, Since: in.DoneAt, Immediate: ready && in.DoneAt.IsZero()}
	}
	ready := len(verdict.RecordedIn(in.Records, in.Head)) > 0 && !blocked
	return Readiness{Ready: ready, Since: reviewReadySince(in.Records, in.Head)}
}

// TrainBlocked is true when the recorded verdicts would block the gate or
// bounce the PR: a FAIL (review or second opinion) or a review with a
// blocking finding keeps it out of a train, a clean CONCERNS and PASS let it in.
func TrainBlocked(records []verdict.Record) bool {
	_, blocked := verdict.BlockingAnyHead(records)
	return blocked
}

// reviewReadySince uses the last current-head review prerequisite, so an old
// PR or a review of a previous head cannot make a fresh review expire early.
func reviewReadySince(records []verdict.Record, head string) time.Time {
	var since time.Time
	list := verdict.ForHead(records, head)
	for _, kind := range append([]string{verdict.ReviewSpec, verdict.ReviewQuality, verdict.ReviewCross, verdict.SecondOpinion}, verdict.LensKinds...) {
		r, ok := verdict.Latest(list, kind)
		if !ok || !verdict.AtHead(r, head) {
			continue
		}
		if at, err := stamp.Parse(r.RecordedAt); err == nil && at.After(since) {
			since = at
		}
	}
	return since
}
