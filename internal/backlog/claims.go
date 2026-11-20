package backlog

import (
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker"
)

// ReclaimedBranchClaim requires a branch-agent claim, its release, then a
// different still-open claim, in that order. A bare release or an existing
// best-of peer is not evidence that the branch was reclaimed.
func ReclaimedBranchClaim(comments []tracker.Comment, agent string, held []string) bool {
	claimed := map[string]bool{}
	released := false
	for _, ev := range marker.FoldClaims(tracker.ClaimComments(comments)).Events {
		holder, _, _ := strings.Cut(ev.ID, "@")
		if holder == agent && agent != "" {
			if ev.Verb == marker.KindClaim {
				claimed[ev.ID] = true
			}
			if ev.Verb == marker.KindRelease && claimed[ev.ID] {
				delete(claimed, ev.ID)
				released = true
			}
		} else if ev.Verb == marker.KindClaim && released && slices.Contains(held, ev.ID) {
			return true
		}
	}
	return false
}
