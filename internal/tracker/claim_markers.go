package tracker

import (
	"context"

	"github.com/l4ci/rota/internal/marker"
)

// ClaimReader reads an issue, including comments when no separate comment
// reader is available.
type ClaimReader interface {
	Get(context.Context, IssueKey, bool) (Issue, error)
}

// ClaimBackend is what a tracker needs to keep claims as marker comments.
type ClaimBackend interface {
	ClaimReader
	AddComment(ctx context.Context, key IssueKey, body string) (string, error)
}

// HeldClaims returns the open claims on key in claim order: the tracker's own
// claim store when it has one, else its claim and release marker comments. A
// separate comment reader avoids an extra issue read.
func HeldClaims(ctx context.Context, tr ClaimReader, key IssueKey) ([]string, error) {
	if cs, ok := tr.(ClaimStore); ok {
		return cs.Claims(ctx, key)
	}
	var comments []Comment
	var err error
	if reader, ok := tr.(interface {
		Comments(context.Context, IssueKey) ([]Comment, error)
	}); ok {
		comments, err = reader.Comments(ctx, key)
	} else {
		var is Issue
		is, err = tr.Get(ctx, key, true)
		comments = is.Comments
	}
	if err != nil {
		return nil, err
	}
	return openClaims(comments), nil
}

// HeldClaimsIn is HeldClaims for a caller that already read the issue's
// comments: a marker tracker answers from them, with no second read.
func HeldClaimsIn(ctx context.Context, tr ClaimReader, key IssueKey, comments []Comment) ([]string, error) {
	if cs, ok := tr.(ClaimStore); ok {
		return cs.Claims(ctx, key)
	}
	return openClaims(comments), nil
}

// openClaims is the claim ids with no later release, in claim order.
func openClaims(comments []Comment) []string {
	bodies := make([]string, 0, len(comments))
	for _, c := range comments {
		bodies = append(bodies, c.Body)
	}
	return marker.OpenClaims(bodies)
}

// ClaimsOf is the claim surface of tr, which every tracker has: its own
// ClaimStore when it implements one, else claims kept as marker comments. A
// caller claims, lists and releases through it and never branches on the
// adapter.
func ClaimsOf(tr ClaimBackend) ClaimStore {
	if cs, ok := tr.(ClaimStore); ok {
		return cs
	}
	return markerClaims{tr}
}

// markerClaims keeps claims as marker comments on a tracker with no claim
// record of its own.
type markerClaims struct{ tr ClaimBackend }

func (m markerClaims) Claims(ctx context.Context, key IssueKey) ([]string, error) {
	return HeldClaims(ctx, m.tr, key)
}

func (m markerClaims) PutClaim(ctx context.Context, key IssueKey, id string) error {
	_, err := m.tr.AddComment(ctx, key, marker.Claim(id)+"\n"+claimText+id)
	return err
}

func (m markerClaims) DropClaim(ctx context.Context, key IssueKey, id string, byHand bool) error {
	body := marker.Release(id)
	if byHand {
		body = marker.HandRelease(id)
	}
	_, err := m.tr.AddComment(ctx, key, body)
	return err
}
