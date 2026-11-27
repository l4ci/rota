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

// ClaimState is the authoritative claim read: held ids, their server times,
// releases chosen by a person, and ordered claim/release events.
type ClaimState = marker.ClaimState
type ClaimEvent = marker.ClaimEvent

// ReadClaims reads the tracker's claim surface, including its history.
func ReadClaims(ctx context.Context, tr ClaimReader, key IssueKey) (ClaimState, error) {
	if cs, ok := tr.(ClaimStore); ok {
		return cs.ClaimState(ctx, key)
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
		return ClaimState{}, err
	}
	return markerClaimState(comments), nil
}

// ReadClaimsIn reuses already-read comments for marker trackers.
func ReadClaimsIn(ctx context.Context, tr ClaimReader, key IssueKey, comments []Comment) (ClaimState, error) {
	if cs, ok := tr.(ClaimStore); ok {
		return cs.ClaimState(ctx, key)
	}
	return markerClaimState(comments), nil
}

// HeldClaims lists held ids in claim order through the same claim surface.
func HeldClaims(ctx context.Context, tr ClaimReader, key IssueKey) ([]string, error) {
	st, err := ReadClaims(ctx, tr, key)
	return st.Held, err
}

func claimCommentsForFold(comments []Comment) []marker.ClaimComment {
	out := make([]marker.ClaimComment, 0, len(comments))
	for _, c := range comments {
		out = append(out, marker.ClaimComment{Body: c.Body, At: c.CreatedAt})
	}
	return out
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

func (m markerClaims) ClaimState(ctx context.Context, key IssueKey) (ClaimState, error) {
	return ReadClaims(ctx, m.tr, key)
}

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

func markerClaimState(comments []Comment) ClaimState {
	st := marker.FoldClaims(claimCommentsForFold(comments))
	for id, at := range st.ClaimedAt {
		if at.IsZero() {
			delete(st.ClaimedAt, id)
		}
	}
	return st
}
