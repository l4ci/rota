package backlog

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
)

// `item create`, `item show` and `item complete` against an It's a Plan
// project: the fields block lives in the rota custom field, done moves the
// issue to the completed column, reopen back to an unstarted one.
func TestItsaplanCreateCompleteRoundTrip(t *testing.T) {
	s := itsaplantest.New(t, itsaplantest.Options{})
	it := tracker.NewItsaplan(connections.Connection{Name: "plan", Kind: connections.KindItsaplan, Site: s.URL}, s.Key(), "MKT")
	b := &Issues{Cfg: mustDecode(t, `{}`), Tracker: it, CountProof: stubCountProof}

	res, err := b.Create(CreateInput{Kind: "tasks", Title: "Widen the seam", Desc: "details\n\n## Acceptance\n- [ ] works\n", Fields: []Field{{Name: "Related", Value: "#5"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.Field("MKT-1"), "rota:fields") {
		t.Errorf("no fields block in the rota field: %q (id %s)", s.Field("MKT-1"), res.ID)
	}
	item, err := b.Get("MKT-1")
	if err != nil || item.Title != "Widen the seam" {
		t.Fatalf("%+v %v", item, err)
	}
	if changed, err := b.Complete("MKT-1", CompleteInput{Reason: "done", Commit: "abc1234", Date: "2026-10-20", NoProof: true}); err != nil || !changed {
		t.Fatalf("complete: %v %v", changed, err)
	}
	if got := s.Column("MKT-1"); got != 4 {
		t.Errorf("done moved the issue to column %d, want the completed one", got)
	}
	if changed, err := b.Reopen("MKT-1"); err != nil || !changed || s.Column("MKT-1") != 2 {
		t.Errorf("reopen: %v %v column %d", changed, err, s.Column("MKT-1"))
	}
}
