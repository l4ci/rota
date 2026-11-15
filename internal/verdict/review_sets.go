package verdict

import "slices"

// Each wider set extends the narrower one. A review kind is declared once;
// the differences between coverage, bounce judging and readiness stay explicit.
var (
	coreReviewKinds      = []string{ReviewSpec, ReviewQuality}
	bounceReviewKinds    = append(append(slices.Clone(coreReviewKinds), ReviewCross), LensKinds...)
	readinessReviewKinds = append(slices.Clone(bounceReviewKinds), SecondOpinion)
)

// CoreReviewsAtHead returns spec and quality coverage at head, applying the
// local-over-recovered-report precedence shared by review and readiness.
func CoreReviewsAtHead(records []Record, head string) []Record {
	return latestAtHead(ForHead(records, head), coreReviewKinds, head)
}

// ReadinessReviewsAtHead returns the current-head prerequisites that date merge
// readiness: core reviews, cross-harness review, lenses and second opinion.
// QA and behaviour do not date readiness. Recovered reports obey ForHead.
func ReadinessReviewsAtHead(records []Record, head string) []Record {
	return latestAtHead(ForHead(records, head), readinessReviewKinds, head)
}

// BounceReviewsAtHead returns the reviews that judge whether a bounce has
// findings to relay: core reviews, cross-harness review and lenses. Second
// opinion is excluded. Recovered reports obey ForHead, as they do at the gate.
func BounceReviewsAtHead(records []Record, head string) []Record {
	return latestAtHead(ForHead(records, head), bounceReviewKinds, head)
}

// LatestBranchRecords returns one latest record per branch kind in display
// order. Show and report delivery include stale records: show labels them,
// while delivery retries reports even after the branch moves on.
func LatestBranchRecords(records []Record) []Record {
	return latestRecords(records, BranchKinds)
}

// Select the latest record first, then test its head: a newer stale local
// record supersedes an older current-head record of the same kind.
func latestAtHead(records []Record, kinds []string, head string) []Record {
	return slices.DeleteFunc(latestPerHarness(records, kinds), func(r Record) bool { return !AtHead(r, head) })
}

func latestRecords(records []Record, kinds []string) []Record {
	var out []Record
	for _, kind := range kinds {
		if r, ok := Latest(records, kind); ok {
			out = append(out, r)
		}
	}
	return out
}

// latestPerHarness is latestRecords, except a review-cross kind yields the
// newest record of each harness: a newer record from one harness must not
// hide another harness's FAIL, so the blocking and readiness sets see both
// (#3328). Records without a harness count as one.
func latestPerHarness(records []Record, kinds []string) []Record {
	var out []Record
	for _, kind := range kinds {
		if kind != ReviewCross {
			if r, ok := Latest(records, kind); ok {
				out = append(out, r)
			}
			continue
		}
		seen := map[string]bool{}
		for i := len(records) - 1; i >= 0; i-- {
			if r := records[i]; r.Kind == kind && !seen[r.Harness] {
				seen[r.Harness] = true
				out = append(out, r)
			}
		}
	}
	return out
}
