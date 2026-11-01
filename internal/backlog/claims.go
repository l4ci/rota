package backlog

import (
	"context"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker"
)

// ClaimReader reads an issue, including comments when no separate comment
// reader is available. ClaimStore is preferred on trackers that implement it.
type ClaimReader interface {
	Get(context.Context, tracker.IssueKey, bool) (tracker.Issue, error)
}

// HeldClaims returns open claims in claim order, from the tracker's claim store
// or its comment history. A separate comment reader avoids an extra issue read.
func HeldClaims(ctx context.Context, tr ClaimReader, key tracker.IssueKey) ([]string, error) {
	if cs, ok := tr.(tracker.ClaimStore); ok {
		return cs.Claims(ctx, key)
	}
	var comments []tracker.Comment
	var err error
	if reader, ok := tr.(interface {
		Comments(context.Context, tracker.IssueKey) ([]tracker.Comment, error)
	}); ok {
		comments, err = reader.Comments(ctx, key)
	} else {
		var is tracker.Issue
		is, err = tr.Get(ctx, key, true)
		comments = is.Comments
	}
	if err != nil {
		return nil, err
	}
	return openClaims(comments), nil
}

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
