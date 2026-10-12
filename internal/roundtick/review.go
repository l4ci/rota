package roundtick

import (
	"context"
	"fmt"
	"strings"
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
	Slot, PR string
	// Auto is round.reviewLoop auto; MaxBounces is the cap as text.
	Auto       bool
	MaxBounces string
	// SetupErr is a failure to build the sources; it counts as a failed poll.
	SetupErr error
	// Warn reports a failure that stays out of the tick items.
	Warn func(format string, args ...any)
	// Open reports whether the slot's PR is open (manual; a slot without a PR
	// is not open).
	Open func(ctx context.Context) (bool, error)
	// Pending is the summary of the review input waiting on the PR, "" when
	// there is none (manual).
	Pending func(ctx context.Context) (string, error)
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
		return ReviewOutcome{Hold: s.Auto}
	}
	if s.SetupErr != nil {
		return pollFailed(s.SetupErr)
	}
	if !s.Auto {
		open, err := s.Open(ctx)
		if err != nil {
			return pollFailed(err)
		}
		if !open {
			return ReviewOutcome{}
		}
		summary, err := s.Pending(ctx)
		if err != nil {
			return pollFailed(err)
		}
		if summary == "" {
			return ReviewOutcome{}
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
