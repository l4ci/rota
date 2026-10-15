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
	"github.com/l4ci/rota/internal/tracker/lineartest"
)

// Linear closes nothing on a merge that rota can rely on: the gate moves the
// issue to a completed state itself and comments with the PR, then releases
// the claim label. A partial slice stays open.
func TestGateClosesALinearIssueAfterTheMerge(t *testing.T) {
	for _, c := range []struct {
		name, title string
		partial     bool
		wantType    string
	}{
		{name: "the PR names the identifier", title: "ENG-1 widen the seam", wantType: "completed"},
		{name: "a partial slice stays open", title: "ENG-1 first half", partial: true, wantType: "unstarted"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := lineartest.New(t, lineartest.Options{})
			l := tracker.NewLinear(connections.Connection{Name: "work", Kind: connections.KindLinear, Site: s.URL}, "lin_api_test", "ENG")
			labels := []string{"in-progress"}
			if c.partial {
				labels = append(labels, PartialSliceLabel)
			}
			key, err := l.Create(bg, "Widen", "", labels, "")
			if err != nil || key != "ENG-1" {
				t.Fatal(key, err)
			}
			w := newWorld(t, ghURL)
			w.setConfig(`{"issues":{"source":"linear"},"test":{"full":["true"]}}`)
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
				[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"ENG-1","pr":%q}]}`, ghURL)), 0o644)
			w.forge("title", c.title)
			w.forge("head", "w1")
			e := w.env(false)
			e.Issues = func(string, tracker.Settings) (Issues, error) { return l, nil }
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if err != nil || res.Verdict != GatePass {
				t.Fatalf("verdict = %q (%v): %+v", res.Verdict, err, res)
			}
			if got := s.StateType("ENG-1"); got != c.wantType {
				t.Errorf("state type %q, want %q; warnings %v", got, c.wantType, res.Warnings)
			}
			if c.partial != slices.Contains(s.Labels("ENG-1"), "in-progress") {
				t.Errorf("labels %v", s.Labels("ENG-1"))
			}
			named := slices.ContainsFunc(s.CommentBodies("ENG-1"), func(b string) bool { return strings.Contains(b, ghURL) })
			if named == c.partial {
				t.Errorf("a comment names the PR: %v, want %v; comments %q", named, !c.partial, s.CommentBodies("ENG-1"))
			}
		})
	}
}
