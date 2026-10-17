package tracker

import (
	"context"
	"slices"
	"strings"
	"unicode"
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
	slices.SortStableFunc(out, func(a, b Comment) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		if c := len(a.ID) - len(b.ID); c != 0 { // numeric ids: shorter is smaller
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

// Claims lists the claim ids on key, earliest first, each once.
func (c claimComments) Claims(ctx context.Context, key IssueKey) ([]string, error) {
	cs, err := c.claims(ctx, key)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, cm := range cs {
		if id, _ := claimOf(cm.Body); !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// PutClaim posts id's claim comment. A second one changes nothing: the
// earliest comment of an id holds its place.
func (c claimComments) PutClaim(ctx context.Context, key IssueKey, id string) error {
	return c.add(ctx, key, claimText+id)
}

// DropClaim deletes every claim comment of id; none is a no-op.
func (c claimComments) DropClaim(ctx context.Context, key IssueKey, id string) error {
	cs, err := c.claims(ctx, key)
	if err != nil {
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
