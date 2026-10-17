package tracker

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// fakeClaimComments is the seam's two adapter calls over a slice of comments.
type fakeClaimComments struct {
	comments []Comment
	deleted  []string
	delErr   map[string]error
}

func (f *fakeClaimComments) store() claimComments {
	return claimComments{
		list: func(context.Context, IssueKey) ([]Comment, error) { return slices.Clone(f.comments), nil },
		add: func(_ context.Context, _ IssueKey, body string) error {
			f.comments = append(f.comments, Comment{ID: "new", Body: body})
			return nil
		},
		delete: func(_ context.Context, _ IssueKey, id string) error {
			f.deleted = append(f.deleted, id)
			return f.delErr[id]
		},
	}
}

func TestClaimCommentsEarliestOfAnIDWins(t *testing.T) {
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	f := &fakeClaimComments{comments: []Comment{
		{ID: "10", Body: "Claimed by b@1", CreatedAt: t0.Add(time.Second)},
		{ID: "9", Body: "Claimed by a@1", CreatedAt: t0},
		{ID: "11", Body: "Claimed by b@1", CreatedAt: t0.Add(2 * time.Second)},
		{ID: "12", Body: "Claimed by nobody in particular", CreatedAt: t0},
		{ID: "13", Body: "hello", CreatedAt: t0},
	}}
	got, err := f.store().Claims(context.Background(), "K-1")
	if want := []string{"a@1", "b@1"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("Claims = %v, %v; want %v", got, err, want)
	}
}

// Claims written in the same instant order by comment id (numeric ids by
// length first), whatever order the server lists them in.
func TestClaimCommentsTieBreakByCommentID(t *testing.T) {
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	f := &fakeClaimComments{comments: []Comment{
		{ID: "100", Body: "Claimed by c@1", CreatedAt: t0},
		{ID: "9", Body: "Claimed by a@1", CreatedAt: t0},
		{ID: "20", Body: "Claimed by b@1", CreatedAt: t0},
	}}
	got, err := f.store().Claims(context.Background(), "K-1")
	if want := []string{"a@1", "b@1", "c@1"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("Claims = %v, %v; want %v", got, err, want)
	}
}

func TestClaimCommentsDrop(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mk := func() *fakeClaimComments {
		return &fakeClaimComments{comments: []Comment{
			{ID: "1", Body: "Claimed by a@1", CreatedAt: t0},
			{ID: "2", Body: "Claimed by a@1", CreatedAt: t0.Add(time.Second)},
			{ID: "3", Body: "Claimed by b@1", CreatedAt: t0.Add(2 * time.Second)},
		}}
	}

	f := mk()
	if err := f.store().DropClaim(ctx, "K-1", "a@1"); err != nil || !slices.Equal(f.deleted, []string{"1", "2"}) {
		t.Fatalf("DropClaim a@1 deleted %v, %v; want [1 2]", f.deleted, err)
	}

	f = mk()
	if err := f.store().DropClaim(ctx, "K-1", "nobody"); err != nil || len(f.deleted) != 0 {
		t.Fatalf("dropping an absent claim deleted %v, %v", f.deleted, err)
	}

	// a comment already gone is not an error and the rest still go.
	f = mk()
	f.delErr = map[string]error{"1": &Error{Kind: KindNotFound}}
	if err := f.store().DropClaim(ctx, "K-1", "a@1"); err != nil || !slices.Equal(f.deleted, []string{"1", "2"}) {
		t.Fatalf("not-found on delete: deleted %v, %v; want [1 2], nil", f.deleted, err)
	}

	// any other delete failure stops the drop.
	f = mk()
	boom := errors.New("boom")
	f.delErr = map[string]error{"1": boom}
	if err := f.store().DropClaim(ctx, "K-1", "a@1"); !errors.Is(err, boom) || !slices.Equal(f.deleted, []string{"1"}) {
		t.Fatalf("delete failure: deleted %v, %v; want [1], boom", f.deleted, err)
	}
}
