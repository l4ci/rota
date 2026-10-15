package tracker

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/tracker/lineartest"
)

func TestLinearClaimsArePlainComments(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	k := linearCreate(t, l, "claimed", "body")
	var cs ClaimStore = l
	for _, id := range []string{"nia@2", "ben@1", "a_b*c"} {
		if err := cs.PutClaim(ctx, k, id); err != nil {
			t.Fatalf("PutClaim %s: %v", id, err)
		}
	}
	// a second claim by the same id keeps its first place.
	if err := cs.PutClaim(ctx, k, "nia@2"); err != nil {
		t.Fatal(err)
	}
	s.AddHumanComment(string(k), "Claimed by x y", "pat")
	got, err := cs.Claims(ctx, k)
	if want := []string{"nia@2", "ben@1", "a_b*c"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("Claims = %v, %v; want %v", got, err, want)
	}
	for _, raw := range s.CommentBodies(string(k)) {
		if strings.Contains(raw, "<!--") || strings.Contains(raw, "rota:") {
			t.Errorf("a claim carries a marker: %s", raw)
		}
	}
	if a := s.Attachments(string(k)); len(a) != 0 {
		t.Errorf("claims left attachments %v", a)
	}
	if err := cs.DropClaim(ctx, k, "nia@2"); err != nil {
		t.Fatal(err)
	}
	if err := cs.DropClaim(ctx, k, "nobody"); err != nil {
		t.Fatalf("dropping an absent claim: %v", err)
	}
	if got, _ := cs.Claims(ctx, k); !slices.Equal(got, []string{"ben@1", "a_b*c"}) {
		t.Fatalf("after release: %v", got)
	}
	if n := len(s.CommentBodies(string(k))); n != 3 {
		t.Fatalf("release left %d comments, want 3 (two claims and the person's)", n)
	}
}

// Claims written in the same instant order by comment id, whatever order the
// server lists them in.
func TestLinearClaimTieBreaksByCommentID(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	k := linearCreate(t, l, "tie", "")
	s.SetNextCommentTime(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), 2)
	for _, id := range []string{"a@1", "b@1"} {
		if err := l.PutClaim(ctx, k, id); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := l.Claims(ctx, k); err != nil || !slices.Equal(got, []string{"a@1", "b@1"}) {
		t.Fatalf("Claims = %v, %v; want [a@1 b@1]", got, err)
	}
}
