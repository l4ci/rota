package tracker

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/l4ci/rota/internal/marker"
)

// claimText opens a claim comment: "Claimed by <claim id>", with no marker,
// so people read it as written. Claims order by the server's created time,
// then comment id, never the claimer's clock: a write that a retry delays
// lands, and counts, after one the server took in between.
const claimText = "Claimed by "

// claimOf is the claim id of a claim comment; ok is false for any other.
func claimOf(body string) (string, bool) {
	id, ok := strings.CutPrefix(strings.TrimSpace(body), claimText)
	if !ok || id == "" || strings.IndexFunc(id, unicode.IsSpace) >= 0 {
		return "", false
	}
	return id, true
}

// claimComments keeps claims as plain comments on a tracker with no claim
// record of its own. The adapter supplies only the comment fetch and delete;
// the claim rules (earliest comment of an id holds its place, a drop takes
// every comment of the id, a comment already gone is not an error) live here.
type claimComments struct {
	list   func(ctx context.Context, key IssueKey) ([]Comment, error)
	add    func(ctx context.Context, key IssueKey, body string) error
	delete func(ctx context.Context, key IssueKey, commentID string) error
}

// claims is the issue's claim comments, earliest first.
func (c claimComments) claims(ctx context.Context, key IssueKey) ([]Comment, error) {
	cs, err := c.list(ctx, key)
	if err != nil {
		return nil, err
	}
	out := slices.DeleteFunc(cs, func(c Comment) bool { _, ok := claimOf(c.Body); return !ok })
	sortClaimComments(out)
	return out, nil
}

func sortClaimComments(cs []Comment) {
	slices.SortStableFunc(cs, func(a, b Comment) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		if c := len(a.ID) - len(b.ID); c != 0 { // numeric ids: shorter is smaller
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
}

// ClaimState reads plain claims and release markers. Marker claims on a
// claim-store tracker are not authoritative ownership records.
func (c claimComments) ClaimState(ctx context.Context, key IssueKey) (ClaimState, error) {
	cs, err := c.list(ctx, key)
	if err != nil {
		return ClaimState{}, err
	}
	sortClaimComments(cs)
	var history []Comment
	var folded []marker.ClaimComment
	var held []string
	times := map[string]time.Time{}
	for _, cm := range cs {
		if id, ok := claimOf(cm.Body); ok {
			history = append(history, Comment{ID: cm.ID, Body: marker.Claim(id), CreatedAt: cm.CreatedAt})
			if !slices.Contains(held, id) {
				held = append(held, id)
				if !cm.CreatedAt.IsZero() {
					times[id] = cm.CreatedAt
				}
			}
		} else if verb, _, ok := marker.ParseClaim(cm.Body); ok && verb == marker.KindRelease {
			history = append(history, cm)
			if id, at, commentID, ok := marker.ParseClaimHistory(cm.Body); ok {
				history = append(history, Comment{ID: commentID, Body: marker.Claim(id), CreatedAt: at})
			}
		}
	}
	sortClaimComments(history)
	for _, cm := range history {
		folded = append(folded, marker.ClaimComment{Body: cm.Body, At: cm.CreatedAt})
	}
	st := marker.FoldClaims(folded)
	// Failed deletion can leave a released comment present. Presence remains
	// authoritative until deletion completes, as it is for the id-only read.
	st.Held, st.ClaimedAt = held, times
	st.HandReleased = slices.DeleteFunc(st.HandReleased, func(id string) bool { return slices.Contains(held, id) })
	return st, nil
}

// Claims lists the claim ids on key, earliest first, each once.
func (c claimComments) Claims(ctx context.Context, key IssueKey) ([]string, error) {
	st, err := c.ClaimState(ctx, key)
	return st.Held, err
}

// PutClaim posts id's claim comment. A second one changes nothing: the
// earliest comment of an id holds its place.
func (c claimComments) PutClaim(ctx context.Context, key IssueKey, id string) error {
	return c.add(ctx, key, claimText+id)
}

// DropClaim records the release before deleting every claim comment of id.
// A failed marker write leaves the claim held, so reconcile cannot mistake a
// deliberate release for a lost claim. None held is a no-op.
func (c claimComments) DropClaim(ctx context.Context, key IssueKey, id string, byHand bool) error {
	cs, err := c.claims(ctx, key)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(cs, func(cm Comment) bool { got, _ := claimOf(cm.Body); return got == id }) {
		return nil
	}
	body := marker.Release(id)
	if byHand {
		body = marker.HandRelease(id)
	}
	for _, cm := range cs {
		if got, _ := claimOf(cm.Body); got == id {
			body += "\n" + marker.ClaimHistory(id, cm.CreatedAt, cm.ID)
			break
		}
	}
	if err := c.add(ctx, key, body); err != nil {
		return err
	}
	for _, cm := range cs {
		if got, _ := claimOf(cm.Body); got == id {
			if err := c.delete(ctx, key, cm.ID); err != nil && !IsKind(err, KindNotFound) {
				return err
			}
		}
	}
	return nil
}
