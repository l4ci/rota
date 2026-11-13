package roundtick

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// RelayResult is what one relay of a slot's review input reported. AtCap is set
// when the item is at the bounce cap: the relay refused, and Items says how
// much input waits.
type RelayResult struct {
	Nothing bool
	AtCap   bool
	Items   int
	Bounces int
}

// ReviewSources is where the review loop reads from. The caller wires the
// forge, the registry and the config into it; the decision between manual and
// auto, and every hold, live in ReviewStep.
type ReviewSources struct {
	// Observe reads pending input without invoking Relay.
	Observe  bool
	Slot, PR string
	// Auto is round.reviewLoop auto; MaxBounces is the cap as text.
	Auto       bool
	MaxBounces string
	// SetupErr is a failure to build the sources; it counts as a failed poll.
	SetupErr error
	// Warn reports a failure that stays out of the tick items.
	Warn func(format string, args ...any)
	// Poll is the summary of the review input waiting on the slot's open PR
	// (manual); "" when there is none, or the PR is not open.
	Poll func(ctx context.Context) (string, error)
	// Relay hands the input to the worker (auto). A relay that cannot go out
	// returns its error; at the cap it sets AtCap.
	Relay func(ctx context.Context) (RelayResult, error)
}

// ReviewStep is one pass of the review loop over a done slot with a PR: it
// looks for review input and, under auto, relays it to the worker. Under
// manual the input is only reported and the merge is not held: the
// orchestrator runs review-relay, the gate runs as before. Under auto a relay
// that cannot go out (the item is at the bounce cap, the host refused) or a
// poll that failed holds the slot, so the autopilot never merges over review
// input it could not hand back. A failed poll is a warning either way, never a
// silent pass and never a tick item.
func ReviewStep(ctx context.Context, s ReviewSources) ReviewOutcome {
	// The detail stays the same from pass to pass, or the watch would wake on it
	// every time; the error text goes to stderr.
	pollFailed := func(err error) ReviewOutcome {
		s.Warn("review poll of %s: %v", s.Slot, err)
		if s.Observe {
			return ReviewOutcome{Hold: s.Auto, Unknown: "review input could not be read: " + err.Error()}
		}
		return ReviewOutcome{Hold: s.Auto}
	}
	if s.SetupErr != nil {
		return pollFailed(s.SetupErr)
	}
	if !s.Auto || s.Observe {
		summary, err := s.Poll(ctx)
		if err != nil {
			return pollFailed(err)
		}
		if summary == "" {
			return ReviewOutcome{}
		}
		if s.Auto {
			return ReviewOutcome{Proposed: true, Detail: summary}
		}
		return ReviewOutcome{Pending: true, Detail: summary}
	}
	res, err := s.Relay(ctx)
	switch {
	case err == nil && res.Nothing:
		return ReviewOutcome{}
	case err == nil:
		return ReviewOutcome{Relayed: true, Detail: fmt.Sprintf("%d item(s), bounce %d of %s %s", res.Items, res.Bounces, s.MaxBounces, s.PR)}
	case res.AtCap:
		return ReviewOutcome{Pending: true, Hold: true, Detail: fmt.Sprintf("%d item(s) waiting on %s; the item is at the bounce cap", res.Items, s.PR)}
	case res.Items == 0: // the poll itself failed
		return pollFailed(err)
	}
	s.Warn("review poll of %s: %v", s.Slot, err)
	why := fmt.Sprintf("%d item(s) not relayed", res.Items)
	return ReviewOutcome{Pending: true, Hold: true, Detail: strings.TrimSpace(fmt.Sprintf("%s %s: run `rota round review-relay %s` for the error", why, s.PR, s.Slot))}
}

// PendingSummary resolves, in one call, whether slot s's PR is open on the
// forge and what review input waits on it: the "<n> from <authors> <pr>"
// summary, "" when the slot is gone or has no PR, the PR is not open, or
// nothing waits. v is a FAIL verdict recorded for the slot's branch, nil for
// none.
func PendingSummary(ctx context.Context, f round.ReviewForge, s *worker.Slot, v *worker.ReviewVerdict, now time.Time) (string, error) {
	if s == nil {
		return "", nil
	}
	n, _ := worker.PRRefNumber(s.PR())
	info, err := f.PRView(ctx, n)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(info.State, "OPEN") {
		return "", nil
	}
	// A FAIL recorded at a head the PR has moved past is not review input (#3162).
	if v != nil && !verdictAtHead(v, info.HeadSHA) {
		v = nil
	}
	b, err := worker.PendingReview(ctx, f, s, v, now)
	if err != nil || b.Empty() {
		return "", err
	}
	return reviewSummary(b), nil
}

// reviewSummary is "<n> from <authors>", the value of a slot's review key.
func reviewSummary(b worker.ReviewBatch) string {
	n := len(b.Items)
	from := strings.Join(b.Authors(), ", ")
	if b.Verdict != "" {
		if n == 0 {
			return "review verdict FAIL " + b.PR
		}
		from += " and a FAIL verdict"
	}
	return fmt.Sprintf("%d from %s %s", n, from, b.PR)
}

// WaitingForReview is the tick line for a done slot whose head lacks review
// verdicts. gateErr is the gate's review-missing refusal ("GATE ben refused —
// <what is missing>; nothing landed"); no gate ran for the slot, so the line
// keeps only what is missing and says what to do.
func WaitingForReview(gateErr, branch string) string {
	line, _, _ := strings.Cut(gateErr, "\n")
	if _, rest, ok := strings.Cut(line, " — "); ok {
		line = rest
	}
	line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "; nothing landed"))
	return fmt.Sprintf("waiting for review verdicts at head (%s); run /rota-review on %s", line, branch)
}

// ReviewReadinessOf classifies the review-only gate probe of a done slot: why it
// still waits for required reviews, and whether its state must go straight to
// the real gate. The caller passes the gate's raw answer. An incomplete probe
// or a read error cannot confirm readiness.
func ReviewReadinessOf(r worker.GateResult, err error, slot string) (why string, gateNow bool) {
	if err != nil {
		return "cannot confirm review verdicts at head: " + strutil.FirstLine(err.Error()), false
	}
	switch r.Verdict {
	case worker.GateStale, worker.GatePRMismatch, worker.GateAlreadyMerged, worker.GateMergedRemotely:
		// These describe the branch or PR, not missing review verdicts. The
		// real gate sends stale work back or handles the PR's current state.
		return "", true
	case worker.GateReviewMissing:
		return WaitingForReview(r.Err, strutil.FirstNonEmpty(r.Branch, slot)), false
	case worker.GateFresh:
		return "", false
	}
	return "cannot confirm review verdicts at head: " + strutil.FirstNonEmpty(strutil.FirstLine(r.Err), r.Verdict), false
}

// RelayResultOf maps a review relay's raw answer to the tick's: a refusal for
// the bounce cap (blockedby.MaxBounces) marks the item AtCap.
func RelayResultOf(nothing bool, items, bounces int, err error) RelayResult {
	out := RelayResult{Nothing: nothing, Items: items, Bounces: bounces}
	if bd, ok := exitcode.DataOf[worker.BlockData](err); ok && bd.BlockedBy == blockedby.MaxBounces {
		out.AtCap = true
	}
	return out
}

// verdictAtHead: some failing review was recorded at head. An unknown head on
// either side keeps the verdict, so a forge that reports no head changes nothing.
func verdictAtHead(v *worker.ReviewVerdict, head string) bool {
	if head == "" || len(v.Shas) == 0 {
		return true
	}
	for _, sha := range v.Shas {
		if sha != "" && (strings.HasPrefix(head, sha) || strings.HasPrefix(sha, head)) {
			return true
		}
	}
	return false
}
