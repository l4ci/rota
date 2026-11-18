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
	for _, comment := range comments {
		verb, id, ok := marker.ParseClaim(comment.Body)
		if !ok {
			continue
		}
		holder, _, _ := strings.Cut(id, "@")
		if holder == agent && agent != "" {
			if verb == marker.KindClaim {
				claimed[id] = true
			}
			if verb == marker.KindRelease && claimed[id] {
				delete(claimed, id)
				released = true
			}
		} else if verb == marker.KindClaim && released && slices.Contains(held, id) {
			return true
		}
	}
	return false
}
