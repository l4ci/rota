package tracker

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
	"github.com/l4ci/rota/internal/tracker/jiratest"
	"github.com/l4ci/rota/internal/tracker/lineartest"
)

// commentTracker is a tracker with no claim store: comments only.
type commentTracker struct{ comments []Comment }

func (c *commentTracker) Get(_ context.Context, _ IssueKey, _ bool) (Issue, error) {
	return Issue{Comments: c.comments}, nil
}

func (c *commentTracker) AddComment(_ context.Context, _ IssueKey, body string) (string, error) {
	id := strconv.Itoa(len(c.comments) + 1)
	c.comments = append(c.comments, Comment{ID: id, Body: body, CreatedAt: time.Date(2026, 1, 1, 0, 0, len(c.comments)+1, 0, time.UTC)})
	return id, nil
}

// TestClaimsOfSeam runs the claim, held-claims and release path through the
// one seam for both claim styles.
func TestClaimsOfSeam(t *testing.T) {
	ctx := context.Background()

	j, _ := fakeJira(t, jiratest.Options{})
	jk, err := j.Create(ctx, "claims", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	l, _ := fakeLinear(t, lineartest.Options{})
	lk := linearCreate(t, l, "claims", "")
	it, _ := fakeItsaplan(t, itsaplantest.Options{})
	ik, err := it.Create(ctx, "claims", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for name, fixture := range map[string]struct {
		tr  ClaimBackend
		key IssueKey
	}{
		"comment markers": {&commentTracker{}, "7"}, "Jira": {j, jk}, "Linear": {l, lk}, "Itsaplan": {it, ik},
	} {
		tr, key := fixture.tr, fixture.key
		t.Run(name, func(t *testing.T) {
			cs := ClaimsOf(tr)
			for _, id := range []string{"ben@1", "dana@2"} {
				if err := cs.PutClaim(ctx, key, id); err != nil {
					t.Fatal(err)
				}
			}
			held, err := HeldClaims(ctx, tr, key)
			if err != nil || !slices.Equal(held, []string{"ben@1", "dana@2"}) {
				t.Fatalf("held %v (%v)", held, err)
			}
			before, err := cs.ClaimState(ctx, key)
			if err != nil || before.ClaimedAt["ben@1"].IsZero() {
				t.Fatalf("missing server time: %+v, %v", before, err)
			}
			originalAt := before.ClaimedAt["ben@1"]
			issue, err := tr.Get(ctx, key, true)
			if err != nil || len(issue.Comments) == 0 || !originalAt.Equal(issue.Comments[0].CreatedAt) {
				t.Fatalf("claim time %v differs from server comments %+v (%v)", originalAt, issue.Comments, err)
			}
			if err := cs.PutClaim(ctx, key, "ben@1"); err != nil {
				t.Fatal(err)
			}
			repeated, err := cs.ClaimState(ctx, key)
			if err != nil || !repeated.ClaimedAt["ben@1"].Equal(originalAt) || !slices.Equal(repeated.Held, []string{"ben@1", "dana@2"}) {
				t.Fatalf("duplicate moved the first claim: %+v, %v", repeated, err)
			}

			if err := cs.DropClaim(ctx, key, "ben@1", true); err != nil {
				t.Fatal(err)
			}
			if held, _ = cs.Claims(ctx, key); !slices.Equal(held, []string{"dana@2"}) {
				t.Fatalf("after release held %v", held)
			}
			released, err := cs.ClaimState(ctx, key)
			if err != nil || !slices.Equal(released.HandReleased, []string{"ben@1"}) || !released.ClaimedAt["ben@1"].IsZero() {
				t.Fatalf("release state %+v, %v", released, err)
			}
			if len(released.Events) < 3 || released.Events[0].ID != "ben@1" || released.Events[0].Verb != marker.KindClaim || !released.Events[0].At.Equal(originalAt) || released.Events[len(released.Events)-1].Verb != marker.KindRelease {
				t.Fatalf("lost original claim history: %+v", released.Events)
			}
			if err := cs.PutClaim(ctx, key, "ben@1"); err != nil {
				t.Fatal(err)
			}
			renewed, err := cs.ClaimState(ctx, key)
			if err != nil || !renewed.ClaimedAt["ben@1"].After(originalAt) || len(renewed.HandReleased) != 0 || !slices.Equal(renewed.Held, []string{"dana@2", "ben@1"}) {
				t.Fatalf("renewed claim state %+v, %v", renewed, err)
			}

		})
	}
	t.Run("marker format", func(t *testing.T) {
		tr := &commentTracker{}
		key := IssueKey("7")
		cs := ClaimsOf(tr)
		_ = cs.PutClaim(ctx, key, "ben@1")
		_ = cs.DropClaim(ctx, key, "ben@1", true)
		if got, want := tr.comments[0].Body, marker.Claim("ben@1")+"\nClaimed by ben@1"; got != want {
			t.Fatalf("claim body %q, want %q", got, want)
		}
		if !marker.IsHandRelease(tr.comments[1].Body, "ben@1") {
			t.Fatalf("release body %q lacks hand-release marker", tr.comments[1].Body)
		}
	})
}

// A peer that claimed before the release must stay before it in history,
// even when the server gives every comment the same timestamp.
func TestClaimHistoryKeepsPeerOrderAtTiedTimes(t *testing.T) {
	ctx := context.Background()
	tr, s := fakeLinear(t, lineartest.Options{})
	key := linearCreate(t, tr, "shared claims", "")
	s.SetNextCommentTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), 3)
	cs := ClaimsOf(tr)
	for _, id := range []string{"ben@1", "dana@2"} {
		if err := cs.PutClaim(ctx, key, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := cs.DropClaim(ctx, key, "ben@1", false); err != nil {
		t.Fatal(err)
	}
	st, err := cs.ClaimState(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Events) != 3 || st.Events[0].ID != "ben@1" || st.Events[1].ID != "dana@2" || st.Events[1].Verb != marker.KindClaim || st.Events[2].Verb != marker.KindRelease {
		t.Fatalf("peer reordered after release: %+v", st.Events)
	}
}
