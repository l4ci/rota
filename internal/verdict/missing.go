package verdict

import (
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/tracker"
)

// reviewKinds are the verdict kinds a review depth needs on record: full both
// reviewers, none nothing, light the Spec reviewer when the branch has a spec
// (an issue) and the Standards reviewer when it has none (#683): the gate
// already runs the mechanical half of Standards, nothing else checks spec drift.
func reviewKinds(d config.ReviewDepth, hasSpec bool) []string {
	switch d {
	case config.DepthFull:
		return []string{ReviewSpec, ReviewQuality}
	case config.DepthLight:
		if hasSpec {
			return []string{ReviewSpec}
		}
		return []string{ReviewQuality}
	}
	return nil
}

// missingReviews is the review kinds depth needs that have does not hold: the
// one owner of the missing-review-verdicts rule.
func missingReviews(depth config.ReviewDepth, hasSpec bool, have []string) []string {
	var missing []string
	for _, k := range reviewKinds(depth, hasSpec) {
		if !slices.Contains(have, k) {
			missing = append(missing, k)
		}
	}
	return missing
}

// CoveredDepth is the highest depth whose review kinds are all in have: what the
// verdicts recorded at head already satisfy.
func CoveredDepth(have []string, hasSpec bool) config.ReviewDepth {
	for _, d := range []config.ReviewDepth{config.DepthFull, config.DepthLight} {
		if len(missingReviews(d, hasSpec, have)) == 0 {
			return d
		}
	}
	return config.DepthNone
}

// HasSpec reports whether issue names a tracker issue, the branch's spec: the
// one derivation every review-depth caller shares.
func HasSpec(issue string) bool {
	_, ok := tracker.ParseKey(issue)
	return ok
}

// DepthRose reports whether required ranks above covered: the depth went up
// after the review ran, so the verdicts on record no longer satisfy it. An
// unknown covered depth ("") never counts as a rise.
func DepthRose(covered, required config.ReviewDepth) bool {
	return covered != "" && required.Outranks(covered)
}

// LensNeed is one triggered review lens: its verdict kind and why it fired.
type LensNeed struct{ Kind, Why string }

// Need is what a review depth asks for at a head: the core kinds, an optional
// cross-harness review and each triggered lens. Cross and Lenses are empty
// for a caller that only asks about the core kinds.
type Need struct {
	Depth   config.ReviewDepth
	HasSpec bool
	// Cross says the depth needs a review-cross verdict; CrossAccept says which
	// harnesses' records satisfy it (nil accepts any).
	Cross       bool
	CrossAccept func(harness string) bool
	Lenses      []LensNeed
}

// Coverage is what the records at a head satisfy of a Need. Core, Cross and
// Lenses name what is still missing; a depth of none asks for nothing, so no
// cross review or lens is ever missing there.
type Coverage struct {
	Core   []string
	Cross  bool
	Lenses []string
}

// Covered reports that nothing is missing.
func (c Coverage) Covered() bool { return len(c.Core) == 0 && !c.Cross && len(c.Lenses) == 0 }

// CoverageAt answers "what does n still need at head" from list: the one owner
// of the covered/missing rule the gate, the cli and the integration check share.
// With a nil list it names everything n asks for.
func CoverageAt(list []Record, head string, n Need) Coverage {
	head = strings.TrimSpace(head)
	cov := Coverage{Core: missingReviews(n.Depth, n.HasSpec, Recorded(list, head))}
	if n.Depth == config.DepthNone {
		return cov
	}
	if n.Cross {
		accept := n.CrossAccept
		if accept == nil {
			accept = func(string) bool { return true }
		}
		_, ok := CrossAtBy(list, head, accept)
		cov.Cross = !ok
	}
	for _, l := range n.Lenses {
		if _, ok := LensAt(list, l.Kind, head); !ok {
			cov.Lenses = append(cov.Lenses, l.Kind)
		}
	}
	return cov
}

// Kinds lists every verdict kind n asks for, in the order the refusal names them.
func (n Need) Kinds() []string {
	kinds := reviewKinds(n.Depth, n.HasSpec)
	if n.Depth == config.DepthNone {
		return kinds
	}
	if n.Cross {
		kinds = append(kinds, ReviewCross)
	}
	for _, l := range n.Lenses {
		kinds = append(kinds, l.Kind)
	}
	return kinds
}
