package tracker

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"github.com/l4ci/rota/internal/marker"
)

// commentTracker is a tracker with no claim store: comments only.
type commentTracker struct{ comments []Comment }

func (c *commentTracker) Get(_ context.Context, _ IssueKey, _ bool) (Issue, error) {
	return Issue{Comments: c.comments}, nil
}

func (c *commentTracker) AddComment(_ context.Context, _ IssueKey, body string) (string, error) {
	id := strconv.Itoa(len(c.comments) + 1)
	c.comments = append(c.comments, Comment{ID: id, Body: body})
	return id, nil
}

// storeTracker adds a native claim store on top.
type storeTracker struct {
	commentTracker
	held []string
}

func (s *storeTracker) Claims(context.Context, IssueKey) ([]string, error) { return s.held, nil }
func (s *storeTracker) PutClaim(_ context.Context, _ IssueKey, id string) error {
	s.held = append(s.held, id)
	return nil
}
func (s *storeTracker) DropClaim(_ context.Context, _ IssueKey, id string, _ bool) error {
	s.held = slices.DeleteFunc(s.held, func(h string) bool { return h == id })
	return nil
}

// TestClaimsOfSeam runs the claim, held-claims and release path through the
// one seam for both claim styles.
func TestClaimsOfSeam(t *testing.T) {
	ctx := context.Background()
	for name, tr := range map[string]interface {
		ClaimBackend
	}{"comment markers": &commentTracker{}, "claim store": &storeTracker{}} {
		t.Run(name, func(t *testing.T) {
			cs := ClaimsOf(tr)
			for _, id := range []string{"ben@1", "dana@2"} {
				if err := cs.PutClaim(ctx, "7", id); err != nil {
					t.Fatal(err)
				}
			}
			held, err := HeldClaims(ctx, tr, "7")
			if err != nil || !slices.Equal(held, []string{"ben@1", "dana@2"}) {
				t.Fatalf("held %v (%v)", held, err)
			}
			if err := cs.DropClaim(ctx, "7", "ben@1", true); err != nil {
				t.Fatal(err)
			}
			if held, _ = cs.Claims(ctx, "7"); !slices.Equal(held, []string{"dana@2"}) {
				t.Fatalf("after release held %v", held)
			}
		})
	}
	t.Run("marker format", func(t *testing.T) {
		tr := &commentTracker{}
		cs := ClaimsOf(tr)
		_ = cs.PutClaim(ctx, "7", "ben@1")
		_ = cs.DropClaim(ctx, "7", "ben@1", true)
		if got, want := tr.comments[0].Body, marker.Claim("ben@1")+"\nClaimed by ben@1"; got != want {
			t.Fatalf("claim body %q, want %q", got, want)
		}
		if !marker.IsHandRelease(tr.comments[1].Body, "ben@1") {
			t.Fatalf("release body %q lacks hand-release marker", tr.comments[1].Body)
		}
	})
}
