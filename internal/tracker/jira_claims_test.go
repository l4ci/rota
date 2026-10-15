package tracker

import (
	"context"
	"encoding/json"
	"slices"
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

func TestJiraClaimsAreProperties(t *testing.T) {
	ctx := context.Background()
	j, s := fakeJira(t, jiratest.Options{})
	clock := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	j.rest.Now = func() time.Time { clock = clock.Add(time.Second); return clock }
	key, err := j.Create(ctx, "claimed", "body", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var cs ClaimStore = j
	for _, id := range []string{"nia@2", "ben@1", "a/b"} {
		if err := cs.PutClaim(ctx, key, id); err != nil {
			t.Fatalf("PutClaim %s: %v", id, err)
		}
	}
	// a second put keeps the claim's place.
	if err := cs.PutClaim(ctx, key, "nia@2"); err != nil {
		t.Fatal(err)
	}
	got, err := cs.Claims(ctx, key)
	if want := []string{"nia@2", "ben@1", "a/b"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("Claims = %v, %v; want %v", got, err, want)
	}
	raw, ok := s.Property(string(key), "rota.claim.ben@1")
	var v struct{ ID, At string }
	if !ok || json.Unmarshal(raw, &v) != nil || v.ID != "ben@1" || v.At == "" {
		t.Fatalf("rota.claim.ben@1 = %s, %v", raw, ok)
	}
	if err := cs.DropClaim(ctx, key, "nia@2"); err != nil {
		t.Fatal(err)
	}
	if err := cs.DropClaim(ctx, key, "nobody"); err != nil {
		t.Fatalf("dropping an absent claim: %v", err)
	}
	if got, _ := cs.Claims(ctx, key); !slices.Equal(got, []string{"ben@1", "a/b"}) {
		t.Fatalf("after release: %v", got)
	}
	if bodies := s.CommentBodies(string(key)); len(bodies) != 0 {
		t.Fatalf("claims wrote comments: %v", bodies)
	}
}

func TestJiraClaimsTieOrdersByID(t *testing.T) {
	ctx := context.Background()
	j, _ := fakeJira(t, jiratest.Options{})
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	j.rest.Now = func() time.Time { return at }
	key, err := j.Create(ctx, "tie", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"zed@1", "amy@1"} {
		if err := j.PutClaim(ctx, key, id); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := j.Claims(ctx, key); !slices.Equal(got, []string{"amy@1", "zed@1"}) {
		t.Fatalf("Claims = %v", got)
	}
}
