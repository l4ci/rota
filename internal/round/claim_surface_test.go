package round

import (
	"context"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
	"github.com/l4ci/rota/internal/tracker/jiratest"
	"github.com/l4ci/rota/internal/tracker/lineartest"
	"github.com/l4ci/rota/internal/worker"
)

func claimSurfaceTrackers() map[string]func(*testing.T) backlog.Tracker {
	return map[string]func(*testing.T) backlog.Tracker{
		"markers": func(t *testing.T) backlog.Tracker { return &timedMarkerTracker{} },
		"Jira": func(t *testing.T) backlog.Tracker {
			s := jiratest.New(t, jiratest.Options{})
			tr, err := tracker.NewJira(connections.Connection{Name: "work", Kind: connections.KindJira, Site: s.URL, User: "me@example.com", Deployment: connections.DeploymentCloud}, "tok", "PROJ", "")
			if err != nil {
				t.Fatal(err)
			}
			return tr
		},
		"Linear": func(t *testing.T) backlog.Tracker {
			s := lineartest.New(t, lineartest.Options{})
			return tracker.NewLinear(connections.Connection{Name: "work", Kind: connections.KindLinear, Site: s.URL}, "lin_api_test", "ENG")
		},
		"Itsaplan": func(t *testing.T) backlog.Tracker {
			s := itsaplantest.New(t, itsaplantest.Options{})
			return tracker.NewItsaplan(connections.Connection{Name: "plan", Kind: connections.KindItsaplan, Site: s.URL}, s.Key(), "MKT")
		},
	}
}

func TestClaimSurfaceStale(t *testing.T) {
	for name, makeTracker := range claimSurfaceTrackers() {
		t.Run(name, func(t *testing.T) {
			tr := makeTracker(t)
			key, err := tr.Create(bg, "Abandoned work", filesBody, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := tracker.ClaimsOf(tr).PutClaim(bg, key, "remote-ben@3"); err != nil {
				t.Fatal(err)
			}
			comments, err := tr.Comments(bg, key)
			if err != nil || len(comments) != 1 {
				t.Fatalf("comments=%v, %v", comments, err)
			}
			at := comments[0].CreatedAt
			if at.IsZero() {
				t.Fatal("fixture must supply a server claim time")
			}
			be := &backlog.Issues{Tracker: tr, Cfg: map[string]any{}}
			forge := (&fakeRemote{}).asForge()
			e := Env{Forge: forge, Worker: wk(worker.Env{Config: worker.NoConfig, Now: func() time.Time { return at.Add(6 * time.Hour) }}),
				Round: roundcfg.Settings{ClaimStaleMinutes: 240}, Git: func(context.Context, string, ...string) (git.Result, error) { return git.Result{}, nil }}
			got, warnings := e.StaleClaims(bg, t.TempDir(), be, []tracker.IssueKey{key})
			if len(got) != 1 || got[0].Holder != "remote-ben@3" || !got[0].Last.Equal(at) || len(warnings) != 0 {
				t.Fatalf("stale=%+v warnings=%v, want holder remote-ben@3 at %v", got, warnings, at)
			}
		})
	}
}

// The in-memory marker adapter has no clock; supply server times at its write boundary.
type timedMarkerTracker struct{ trackertest.Fake }

func (tr *timedMarkerTracker) AddComment(ctx context.Context, key tracker.IssueKey, body string) (string, error) {
	id, err := tr.Fake.AddComment(ctx, key, body)
	if err == nil {
		for i := range tr.Issues {
			if tr.Issues[i].Key == key {
				cs := tr.Issues[i].Comments
				cs[len(cs)-1].CreatedAt = time.Date(2026, 1, 1, 0, 0, len(cs), 0, time.UTC)
			}
		}
	}
	return id, err
}

func TestClaimSurfaceAdoptReclaimed(t *testing.T) {
	for name, makeTracker := range claimSurfaceTrackers() {
		t.Run(name, func(t *testing.T) {
			tr := makeTracker(t)
			key, err := tr.Create(bg, "Reclaimed work", filesBody, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			cs := tracker.ClaimsOf(tr)
			if err := cs.PutClaim(bg, key, "codex@3"); err != nil {
				t.Fatal(err)
			}
			if err := cs.DropClaim(bg, key, "codex@3", true); err != nil {
				t.Fatal(err)
			}
			if err := cs.PutClaim(bg, key, "dana@5"); err != nil {
				t.Fatal(err)
			}
			root, e, _ := adoptFixture(t)
			branch := "codex/" + key.String() + "-thing"
			gittest.Run(t, root, "branch", branch)
			be := &backlog.Issues{Tracker: tr, Cfg: map[string]any{}}
			_, err = e.Adopt(bg, root, be, AdoptOpts{Ref: branch, Issue: key.String()})
			if err == nil {
				st, _ := cs.ClaimState(bg, key)
				comments, _ := tr.Comments(bg, key)
				t.Fatalf("adopted reclaimed branch, want claimed refusal; state=%+v comments=%+v", st, comments)
			}
			if got := blockedBy(t, err); got != blockedby.Claimed {
				t.Fatalf("blockedBy=%q, want claimed: %v", got, err)
			}
			if len(worker.LoadRegistryTolerant(root).Slots()) != 0 {
				t.Fatal("refused adoption registered a slot")
			}
		})
	}
}

func TestClaimSurfaceAdoptFresh(t *testing.T) {
	for name, makeTracker := range claimSurfaceTrackers() {
		for _, scenario := range []string{"fresh holder", "bare release", "existing peer"} {
			t.Run(name+"/"+scenario, func(t *testing.T) {
				tr := makeTracker(t)
				key, err := tr.Create(bg, "Fresh work", filesBody, nil, "")
				if err != nil {
					t.Fatal(err)
				}
				cs := tracker.ClaimsOf(tr)
				if scenario == "existing peer" {
					if err := cs.PutClaim(bg, key, "codex@3"); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "bare release" {
					if _, err := tr.AddComment(bg, key, marker.Release("codex@3")); err != nil {
						t.Fatal(err)
					}
				}
				if err := cs.PutClaim(bg, key, "dana@5"); err != nil {
					t.Fatal(err)
				}
				if scenario == "existing peer" {
					if err := cs.DropClaim(bg, key, "codex@3", false); err != nil {
						t.Fatal(err)
					}
				}
				root, e, _ := adoptFixture(t)
				branch := "codex/" + key.String() + "-thing"
				gittest.Run(t, root, "branch", branch)
				be := &backlog.Issues{Tracker: tr, Cfg: map[string]any{}}
				res, err := e.Adopt(bg, root, be, AdoptOpts{Ref: branch, Issue: key.String()})
				if err != nil || res.Slot != "ext-1" {
					t.Fatalf("fresh work refused: %+v, %v", res, err)
				}
			})
		}
	}
}
