package tracker

import (
	"context"
	"slices"
	"strings"
	"testing"

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
	if in := input(shape(t, s, "CommentCreate", "mutation CommentCreate($input: CommentCreateInput!)", "commentCreate(input: $input) { success comment { id } }")); in["body"] != "Claimed by nia@2" || in["issueId"] == "" {
		t.Errorf("claim comment input = %v", in)
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
	if vars := shape(t, s, "CommentDelete", "mutation CommentDelete($id: String!)", "commentDelete(id: $id) { success }"); vars["id"] == "" {
		t.Errorf("release deleted no comment id: %v", vars)
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
