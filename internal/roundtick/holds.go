package roundtick

import "strings"

// HoldReason is the shared eligibility check for a target and its PR head.
// A new head clears a recorded failure; a failed head follows the PR when its
// slot changes. Legacy holds without a recorded head remain conservative.
func HoldReason(held map[string]string, failed map[string]FailedHead, target, head string) (string, bool) {
	if head == "" {
		why, ok := held[target]
		return why, ok
	}
	contentFailure := func(why string) bool {
		v, _, _ := strings.Cut(why, ":")
		return failedHeadVerdict(v)
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
