package tracker

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/tracker/jiratest"
)

// fakeJira is a Jira adapter on a fresh fake; v is 3 (Cloud) or 2 (Data Center).
func fakeJira(t *testing.T, o jiratest.Options) (*Jira, *jiratest.Server) {
	t.Helper()
	s := jiratest.New(t, o)
	c := connections.Connection{Name: "work", Kind: connections.KindJira, Site: s.URL, User: "me@example.com"}
	if s.Version() == 2 {
		c.User = ""
	}
	j := NewJira(c, "tok", s.Project(), "")
	j.rest.Sleep = func(context.Context, time.Duration) error { return nil }
	return j, s
}

func TestJiraClaimsArePlainComments(t *testing.T) {
	ctx := context.Background()
	j, s := fakeJira(t, jiratest.Options{})
	key, err := j.Create(ctx, "claimed", "body", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var cs ClaimStore = j
	for _, id := range []string{"nia@2", "ben@1", "a_b*c"} {
		if err := cs.PutClaim(ctx, key, id); err != nil {
			t.Fatalf("PutClaim %s: %v", id, err)
		}
	}
	// a second claim by the same id keeps its first place.
	if err := cs.PutClaim(ctx, key, "nia@2"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.AddComment(ctx, key, "Claimed by nobody in particular"); err != nil {
		t.Fatal(err)
	}
	got, err := cs.Claims(ctx, key)
	if want := []string{"nia@2", "ben@1", "a_b*c"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("Claims = %v, %v; want %v", got, err, want)
	}
	for _, raw := range s.CommentBodies(string(key)) {
		if strings.Contains(raw, "<!--") || strings.Contains(raw, "rota:") {
			t.Errorf("a claim carries a marker: %s", raw)
		}
	}
	if err := cs.DropClaim(ctx, key, "nia@2"); err != nil {
		t.Fatal(err)
	}
	if err := cs.DropClaim(ctx, key, "nobody"); err != nil {
		t.Fatalf("dropping an absent claim: %v", err)
	}
	if got, _ := cs.Claims(ctx, key); !slices.Equal(got, []string{"ben@1", "a_b*c"}) {
		t.Fatalf("after release: %v", got)
	}
	if n := len(s.CommentBodies(string(key))); n != 3 {
		t.Fatalf("release left %d comments, want 3 (two claims and the person's)", n)
	}
}

// A claim retried after a 429 lands after a claim the server took in between:
// the order is the server's, never the claimer's clock.
func TestJiraClaimRetryKeepsServerOrder(t *testing.T) {
	ctx := context.Background()
	a, s := fakeJira(t, jiratest.Options{})
	b := NewJira(connections.Connection{Name: "work", Kind: connections.KindJira, Site: s.URL, User: "me@example.com"}, "tok", s.Project(), "")
	key, err := a.Create(ctx, "raced", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	a.rest.Now = func() time.Time { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) } // A's clock is behind
	a.rest.Sleep = func(context.Context, time.Duration) error { return b.PutClaim(ctx, key, "b@1") }
	s.RateLimitNext(1, "1")
	if err := a.PutClaim(ctx, key, "a@1"); err != nil {
		t.Fatal(err)
	}
	if got, err := a.Claims(ctx, key); err != nil || !slices.Equal(got, []string{"b@1", "a@1"}) {
		t.Fatalf("Claims = %v, %v; want b@1 first", got, err)
	}
}
