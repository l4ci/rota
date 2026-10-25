package verdict

import (
	"slices"

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

// MissingReviews is the review kinds depth needs that have does not hold: the
// one owner of the missing-review-verdicts rule.
func MissingReviews(depth config.ReviewDepth, hasSpec bool, have []string) []string {
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
		if len(MissingReviews(d, hasSpec, have)) == 0 {
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
