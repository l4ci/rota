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
	"github.com/l4ci/rota/internal/tracker/jiratest"
)

// Jira does not close an issue when its PR merges: the gate moves it to a
// done status itself, then releases the claim label as for a closed GitHub
// issue. A partial slice stays open.
func TestGateClosesAJiraIssueAfterTheMerge(t *testing.T) {
	for _, c := range []struct {
		name, title string
		partial     bool
		wantStatus  string
	}{
		{name: "the PR names the key", title: "PROJ-1 widen the seam", wantStatus: "Done"},
		{name: "a partial slice stays open", title: "PROJ-1 first half", partial: true, wantStatus: "To Do"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := jiratest.New(t, jiratest.Options{})
			j := tracker.NewJira(connections.Connection{Name: "work", Kind: connections.KindJira, Site: s.URL, User: "me@example.com", Deployment: connections.DeploymentCloud}, "tok", "PROJ", "")
			labels := []string{"in-progress"}
			if c.partial {
				labels = append(labels, PartialSliceLabel)
			}
			key, err := j.Create(bg, "Widen", "", labels, "")
			if err != nil || key != "PROJ-1" {
				t.Fatal(key, err)
			}
			w := newWorld(t, ghURL)
			w.setConfig(`{"issues":{"source":"jira"},"test":{"full":["true"]}}`)
			os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"),
				[]byte(fmt.Sprintf(`{"slots":[{"name":"w1","branch":"w1","task":"PROJ-1","pr":%q}]}`, ghURL)), 0o644)
			w.forge("title", c.title)
			w.forge("head", "w1")
			e := w.env(false)
			e.Issues = func(string, tracker.Settings) (Issues, error) { return j, nil }
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if err != nil || res.Verdict != GatePass {
				t.Fatalf("verdict = %q (%v): %+v", res.Verdict, err, res)
			}
			if got := s.StatusOf("PROJ-1"); got != c.wantStatus {
				t.Errorf("status %q, want %q; notes %v", got, c.wantStatus, res.Notes)
			}
			is, _ := j.Get(bg, key, true)
			if c.partial != slices.Contains(is.Labels, "in-progress") {
				t.Errorf("labels %v", is.Labels)
			}
			if !c.partial && !slices.ContainsFunc(is.Comments, func(cm tracker.Comment) bool { return strings.Contains(cm.Body, ghURL) }) {
				t.Errorf("no comment names the PR: %+v", is.Comments)
			}
		})
	}
}
