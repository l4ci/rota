package round

import (
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
	"github.com/l4ci/rota/internal/tracker/jiratest"
	"github.com/l4ci/rota/internal/tracker/lineartest"
)

func TestClaimStoreHandReleaseSurvivesReconcile(t *testing.T) {
	t.Run("Jira", func(t *testing.T) {
		s := jiratest.New(t, jiratest.Options{})
		tr := tracker.NewJira(connections.Connection{Name: "work", Kind: connections.KindJira, Site: s.URL, User: "me@example.com", Deployment: connections.DeploymentCloud}, "tok", "PROJ", "")
		testClaimStoreHandRelease(t, tr)
	})
	t.Run("Linear", func(t *testing.T) {
		s := lineartest.New(t, lineartest.Options{})
		tr := tracker.NewLinear(connections.Connection{Name: "work", Kind: connections.KindLinear, Site: s.URL}, "lin_api_test", "ENG")
		testClaimStoreHandRelease(t, tr)
	})
	t.Run("Itsaplan", func(t *testing.T) {
		s := itsaplantest.New(t, itsaplantest.Options{})
		tr := tracker.NewItsaplan(connections.Connection{Name: "plan", Kind: connections.KindItsaplan, Site: s.URL}, s.Key(), "MKT")
		testClaimStoreHandRelease(t, tr)
	})
}

func testClaimStoreHandRelease(t *testing.T, tr tracker.IssueTracker) {
	t.Helper()
	f := newMoveFx(t)
	b := &backlog.Issues{Cfg: config.Load(cfgdir.OSEnv(), f.root), Tracker: tr}
	key, err := tr.Create(bg, "Released work", "body", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	ref, claim := key.String(), f.slot("ben").ClaimID()
	if err := rawSlot(f.root, "ben", func(s *jsonx.Object) { s.Set("task", ref) }); err != nil {
		t.Fatal(err)
	}
	f.env.Board = b
	f.forge.labels = nil
	f.forge.labelled = nil
	claimItem := func() {
		t.Helper()
		if won, _, err := b.Claim(ref, claim); err != nil || !won {
			t.Fatalf("claim: won=%v, %v", won, err)
		}
	}
	status := func() *backlog.Status {
		t.Helper()
		st, err := b.Status(ref)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	claimItem()
	if ok, err := b.ReleaseByHand(ref, claim); err != nil || !ok {
		t.Fatalf("hand release: %v, %v", ok, err)
	}
	if st := status(); st.Claim != "" || !slices.Equal(st.HandReleased, []string{claim}) {
		t.Fatalf("hand release must survive the claim store: %+v", st)
	}
	rep, err := f.env.Status(bg, f.root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, fd := range rep.Findings {
		if fd.Kind == ClaimMismatch && fd.Slot == "ben" {
			found = true
			if fd.Repair != "" {
				t.Fatalf("a deliberate release offers no repair: %+v", fd)
			}
		}
	}
	if !found {
		t.Fatal("missing claim-mismatch finding for the deliberately released claim")
	}
	if _, err := f.env.Reconcile(bg, f.root, true); err != nil {
		t.Fatal(err)
	}
	if st := status(); st.Claim != "" || !slices.Equal(st.HandReleased, []string{claim}) {
		t.Fatalf("reconcile must leave a hand release gone: %+v", st)
	}
	claimItem()
	if st := status(); st.Claim != claim || len(st.HandReleased) != 0 {
		t.Fatalf("an explicit later claim clears the hand release: %+v", st)
	}
	if ok, err := b.Release(ref, claim); err != nil || !ok {
		t.Fatalf("ordinary release: %v, %v", ok, err)
	}
	if st := status(); st.Claim != "" || len(st.HandReleased) != 0 {
		t.Fatalf("an ordinary later release must not revive an old hand release: %+v", st)
	}
}
