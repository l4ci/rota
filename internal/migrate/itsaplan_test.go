package migrate

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
)

// A file backlog moves onto an It's a Plan project with its titles, bodies,
// labels and acceptance lines. The project's milestone has no place there: it
// is reported as skipped, not a failure, and a second run changes nothing.
func TestMigrateFileToItsaplan(t *testing.T) {
	root := migProject(t, map[string]string{
		".rota/features/F01.md": "# F01\n\nbody text\n\n## Acceptance\n- [ ] AC-1: it works\n- [ ] AC-2: and again\n\n## Proof\n- ok\n",
	})
	s := itsaplantest.New(t, itsaplantest.Options{})
	it := tracker.NewItsaplan(connections.Connection{Name: "plan", Kind: connections.KindItsaplan, Site: s.URL}, s.Key(), "MKT")
	run := func() (*Result, []string) {
		var warns []string
		res, err := Run(Options{Root: root, Apply: true, Limit: -1, Cfg: jsonx.NewObject(),
			Tracker: func() (Tracker, error) { return it, nil },
			Sleep:   func(time.Duration) {}, Warn: func(w string) { warns = append(warns, w) },
			Today: func() string { return "2026-10-02" }})
		if err != nil {
			t.Fatalf("run: %v (warnings %v)", err, warns)
		}
		return res, warns
	}
	res, warns := run()
	if !res.Done || res.Migrated != 4 {
		t.Fatalf("done %v migrated %d of %d; ops %v", res.Done, res.Migrated, res.Total, res.Ops)
	}
	if !slices.ContainsFunc(warns, func(w string) bool { return strings.Contains(w, "milestone M01 skipped") }) {
		t.Errorf("the skipped milestone was not reported: %v", warns)
	}
	if strings.Contains(strings.Join(warns, "\n"), "shipped/archived") {
		t.Errorf("a skipped milestone is not a shipped one: %v", warns)
	}
	issues, err := it.List(context.Background(), tracker.ListFilter{State: "all"})
	if err != nil || len(issues) != 4 {
		t.Fatalf("%d issues, %v", len(issues), err)
	}
	byTitle := map[string]tracker.Issue{}
	for _, is := range issues {
		byTitle[is.Title] = is
	}
	f := byTitle["Feature"]
	if f.Key == "" || !strings.Contains(f.Body, "- [ ] AC-1: it works") || !strings.Contains(f.Body, "- [ ] AC-2: and again") || !strings.Contains(f.Body, "body text") {
		t.Errorf("feature lost its body or acceptance lines: %q", f.Body)
	}
	if !slices.Contains(f.Labels, "type:feature") || !slices.Contains(byTitle["First bug"].Labels, "type:bug") {
		t.Errorf("labels: feature %v, bug %v", f.Labels, byTitle["First bug"].Labels)
	}
	if strings.Contains(byTitle["First bug"].Body, "Milestone:") {
		t.Errorf("a Milestone field points at a milestone that is not there: %q", byTitle["First bug"].Body)
	}
	if slices.ContainsFunc(res.Lines, func(l string) bool { return strings.Contains(l, "config set issues.source") }) {
		t.Errorf("itsaplan already has issues.source set; the Done banner must not ask for it again: %v", res.Lines)
	}
	before := s.Issues()
	if res, _ = run(); s.Issues() != before || !res.Done {
		t.Errorf("a second run changed the project: %d issues, was %d", s.Issues(), before)
	}
}
