package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
)

func itsaplanGate(t *testing.T, labels []string, title string, prep func(*itsaplantest.Server, *tracker.Itsaplan)) (*itsaplantest.Server, GateResult, error) {
	t.Helper()
	s := itsaplantest.New(t, itsaplantest.Options{})
	it := tracker.NewItsaplan(connections.Connection{Name: "plan", Kind: connections.KindItsaplan, Site: s.URL}, s.Key(), "MKT")
	if key, err := it.Create(bg, "Widen", "", labels, ""); err != nil || key != "MKT-1" {
		t.Fatal(key, err)
	}
	if prep != nil {
		prep(s, it)
	}
	w := newWorld(t, ghURL)
	w.setConfig(`{"issues":{"source":"itsaplan"},"test":{"full":["true"]}}`)
	os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
		[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"MKT-1","pr":%q}]}`, ghURL)), 0o644)
	w.forge("title", title)
	w.forge("head", "w1")
	e := w.env(false)
	e.Issues = func(string, tracker.Settings) (Issues, error) { return it, nil }
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	return s, res, err
}

// The gate moves the issue to a completed column itself and comments with the
// PR, then releases the claim label. A partial slice stays open.
func TestGateClosesAnItsaplanIssueAfterTheMerge(t *testing.T) {
	for _, c := range []struct {
		name, title string
		partial     bool
		wantColumn  int
	}{
		{name: "the PR names the key", title: "MKT-1 widen the seam", wantColumn: 4},
		{name: "a partial slice stays open", title: "MKT-1 first half", partial: true, wantColumn: 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			labels := []string{"in-progress"}
			if c.partial {
				labels = append(labels, PartialSliceLabel)
			}
			s, res, err := itsaplanGate(t, labels, c.title, nil)
			if err != nil || res.Verdict != GatePass {
				t.Fatalf("verdict = %q (%v): %+v", res.Verdict, err, res)
			}
			if got := s.Column("MKT-1"); got != c.wantColumn {
				t.Errorf("column %d, want %d; warnings %v", got, c.wantColumn, res.Warnings)
			}
			if c.partial != slices.Contains(s.Labels("MKT-1"), "in-progress") {
				t.Errorf("labels %v", s.Labels("MKT-1"))
			}
			named := slices.ContainsFunc(s.CommentBodies("MKT-1"), func(b string) bool { return strings.Contains(b, ghURL) })
			if named == c.partial {
				t.Errorf("a comment names the PR: %v, want %v", named, !c.partial)
			}
		})
	}
}

// It's a Plan's own forge webhook may have closed the issue first: the gate
// writes nothing more to it.
func TestGateItsaplanAlreadyClosedNoop(t *testing.T) {
	s, res, err := itsaplanGate(t, nil, "MKT-1 widen the seam", func(s *itsaplantest.Server, it *tracker.Itsaplan) {
		if err := it.Close(bg, "MKT-1", tracker.CloseCompleted, ""); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil || res.Verdict != GatePass || len(res.Warnings) != 0 {
		t.Fatalf("%q %v %+v", res.Verdict, err, res)
	}
	if s.Column("MKT-1") != 4 || len(s.CommentBodies("MKT-1")) != 0 {
		t.Errorf("column %d comments %q", s.Column("MKT-1"), s.CommentBodies("MKT-1"))
	}
}

// A close It's a Plan refuses keeps the claim: the merge has landed, so the gate
// passes, but the issue keeps its claim label for `rota round reconcile` and the
// result carries a warning.
func TestGateKeepsTheClaimWhenTheItsaplanCloseFails(t *testing.T) {
	s, res, err := itsaplanGate(t, []string{"in-progress"}, "MKT-1 widen the seam", func(s *itsaplantest.Server, _ *tracker.Itsaplan) {
		s.Fail("PATCH /issues/1", 403)
	})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("%q %v %+v", res.Verdict, err, res)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "MKT-1") {
		t.Errorf("warnings %v, want one naming MKT-1", res.Warnings)
	}
	if s.Column("MKT-1") != 2 || !slices.Contains(s.Labels("MKT-1"), "in-progress") {
		t.Errorf("column %d labels %v: want open and still claimed", s.Column("MKT-1"), s.Labels("MKT-1"))
	}
}
