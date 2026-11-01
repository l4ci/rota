package tracker

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker/itsaplantest"
)

func TestItsaplanClaimsArePlainComments(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{})
	k, _ := it.Create(ctx, "claimed", "body", nil, "")
	var cs ClaimStore = it
	for _, id := range []string{"nia@2", "ben@1", "a_b*c"} {
		if err := cs.PutClaim(ctx, k, id); err != nil {
			t.Fatalf("PutClaim %s: %v", id, err)
		}
	}
	if err := cs.PutClaim(ctx, k, "nia@2"); err != nil { // keeps its first place
		t.Fatal(err)
	}
	got, err := cs.Claims(ctx, k)
	if want := []string{"nia@2", "ben@1", "a_b*c"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("Claims = %v, %v; want %v", got, err, want)
	}
	for _, raw := range s.CommentBodies("MKT-1") {
		if strings.Contains(raw, "<!--") || strings.Contains(raw, "rota:") {
			t.Errorf("a claim carries a marker: %s", raw)
		}
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
}

// The feed is cursor-paged: a claim on a later page still counts, and the
// earliest claim of an id wins whatever page it is on.
func TestItsaplanClaimBeyondTheFirstFeedPage(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{FeedPage: 2})
	k := IssueKey(s.Add(itsaplantest.Issue{Title: "busy", Comments: []string{"chat", "more chat", "even more", "Claimed by ben@1", "later", "Claimed by nia@2"}}))
	got, err := it.Claims(ctx, k)
	if want := []string{"ben@1", "nia@2"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("Claims = %v, %v; want %v", got, err, want)
	}
	if n := len(feedRequests(s)); n != 3 {
		t.Errorf("%d feed pages read, want 3", n)
	}
	if err := it.DropClaim(ctx, k, "ben@1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := it.Claims(ctx, k); !slices.Equal(got, []string{"nia@2"}) {
		t.Errorf("after drop: %v", got)
	}
}

func TestItsaplanAssignSelf(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{})
	k, _ := it.Create(ctx, "a", "", nil, "")
	if err := it.AssignSelf(ctx, k); err != nil || !s.Assigned("MKT-1") {
		t.Fatalf("%v assigned %v", err, s.Assigned("MKT-1"))
	}
	if is, _ := it.Get(ctx, k, false); !slices.Equal(is.Assignees, []string{"Rota"}) {
		t.Errorf("assignees %v", is.Assignees)
	}
}

// #1148: Edit refused the rota field after it had already written the new
// text; it now checks first, so a refused key leaves the issue as it was.
func TestItsaplanEditChecksTheFieldBeforeWriting(t *testing.T) {
	ctx := context.Background()
	it, s := fakeItsaplan(t, itsaplantest.Options{NoField: true, NoFieldRight: true})
	k, _ := it.Create(ctx, "a", "original", nil, "")
	body, title := "new text\n\n"+fieldsBlock, "new title"
	if err := it.Edit(ctx, k, IssueEdit{Title: &title, Body: &body}); !IsKind(err, KindUnavailable) {
		t.Fatalf("%v", err)
	}
	if got := s.Description("MKT-1"); got != "original" {
		t.Errorf("description %q: the text was written before the refusal", got)
	}
	if is, _ := it.Get(ctx, k, false); is.Title != "a" {
		t.Errorf("title %q was written before the refusal", is.Title)
	}
}
