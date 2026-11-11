package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

func TestWorkerTrainIntegrationFlag(t *testing.T) {
	dir := planProject(t, gateConfig(t, `{"test":{"full":["true"]}}`, "off", nil), false)
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"with --plan", []string{"--plan"}, "--integration cannot be combined"},
		{"with --land-green", []string{"--land-green"}, "--integration cannot be combined"},
		{"with --on-fail stop", []string{"--on-fail", "stop"}, "--integration cannot be combined"},
		{"slots without PRs", nil, "every member needs an open PR"},
		{"a name that is no ref component", []string{"--integration", "a/b"}, "not a valid branch name"},
	} {
		t.Run(c.name, func(t *testing.T) {
			args := append([]string{"worker", "train", "w1", "w2", "--base", "main", "--integration", "a", "--no-assign-check"}, c.args...)
			code, out, errOut := rotaIn(t, dir, args...)
			if code != 2 || !strings.Contains(out+errOut, c.want) {
				t.Fatalf("exit %d, want 2 with %q:\n%s%s", code, c.want, out, errOut)
			}
		})
	}
}

func TestWorkerTrainIntegrationFilesItem(t *testing.T) {
	dir := workerProject(t, `{"test":{"full":["true"]}}`)
	os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644)
	it := worker.Integration{Name: "a", Branch: "integration/r55-a", Round: 55, Base: "main",
		Members: []worker.IntegrationMember{{Target: "7", PR: "https://github.com/o/r/pull/7", Issue: "5"}, {Target: "8", PR: "https://github.com/o/r/pull/8", Issue: "6", Pending: true, Conflicts: []string{"work.txt"}}}}
	if err := worker.RecordIntegration(dir, it); err != nil {
		t.Fatal(err)
	}
	be := &backlog.File{Root: dir}
	r := worker.TrainResult{Verdict: worker.GateInteraction, Interaction: []string{"8", "7"}, Integration: &it,
		Err: "TRAIN-INTERACTION 8, 7 — w2 does not merge onto integration/r55-a"}
	id, filed, err := fileInteractionItem(be, dir, r)
	if err != nil || !filed || id == "" {
		t.Fatalf("first interaction: %q %v %v", id, filed, err)
	}
	item, err := be.Get(id)
	if err != nil || item == nil {
		t.Fatalf("item %s: %v", id, err)
	}
	if rel := item.Fields.Related; !strings.Contains(rel, "#5") || !strings.Contains(rel, "#6") {
		t.Errorf("item not linked to the member issues: %q", rel)
	}
	body, _, _ := be.Detail(id)
	for _, want := range []string{"integration/r55-a", "work.txt", "--integration a"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	reg, _ := worker.LoadRegistry(dir)
	if rec, ok := reg.IntegrationForItem(id); !ok || rec.Branch != it.Branch {
		t.Fatalf("item not recorded on the branch: %+v %v", rec, ok)
	}
	// The same interaction again reuses the open item rota recorded.
	reg, _ = worker.LoadRegistry(dir)
	rec, _ := reg.IntegrationByBranch(it.Branch)
	r.Integration = &rec
	again, filed, err := fileInteractionItem(be, dir, r)
	if err != nil || filed || again != id {
		t.Fatalf("rerun filed again: %q %v %v", again, filed, err)
	}
	reg, _ = worker.LoadRegistry(dir)
	if rec, _ := reg.IntegrationByBranch(it.Branch); len(rec.Items) != 1 {
		t.Fatalf("item recorded twice: %v", rec.Items)
	}
}

// An open item that only shares the title was not filed by rota for this
// branch: it is never adopted as the interaction item.
func TestWorkerTrainIntegrationIgnoresTitleTwin(t *testing.T) {
	dir := workerProject(t, `{"test":{"full":["true"]}}`)
	os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644)
	it := worker.Integration{Name: "a", Branch: "integration/r55-a", Round: 55, Base: "main",
		Members: []worker.IntegrationMember{{Target: "7", Issue: "5"}, {Target: "8", Issue: "6"}}}
	if err := worker.RecordIntegration(dir, it); err != nil {
		t.Fatal(err)
	}
	be := &backlog.File{Root: dir}
	twin, err := be.Create(backlog.CreateInput{Kind: "bugs", Tag: "P1", Title: interactionTitle(it, []string{"8", "7"})})
	if err != nil {
		t.Fatal(err)
	}
	r := worker.TrainResult{Verdict: worker.GateInteraction, Interaction: []string{"8", "7"}, Integration: &it, Err: "TRAIN-INTERACTION"}
	id, filed, err := fileInteractionItem(be, dir, r)
	if err != nil || !filed || id == twin.ID {
		t.Fatalf("adopted the title twin %s: %q %v %v", twin.ID, id, filed, err)
	}
}

type prSpy struct{ specs []tracker.PRSpec }

func (*prSpy) Provider() string  { return "github" }
func (*prSpy) PRNeedsBase() bool { return true }
func (p *prSpy) PRCreate(_ context.Context, s tracker.PRSpec) (string, error) {
	p.specs = append(p.specs, s)
	return "https://github.com/o/r/pull/9\n", nil
}

func TestWorkerTrainIntegrationOpensPR(t *testing.T) {
	dir := workerProject(t, `{"test":{"full":["true"]}}`)
	it := worker.Integration{Name: "a", Branch: "integration/r55-a", Round: 55, Base: "main", VerifiedTip: "abc",
		Members: []worker.IntegrationMember{{Target: "7", PR: "https://github.com/o/r/pull/7", Issue: "5"}, {Target: "8", PR: "https://github.com/o/r/pull/8", Issue: "#6"}}}
	if err := worker.RecordIntegration(dir, it); err != nil {
		t.Fatal(err)
	}
	spy := &prSpy{}
	url, err := openIntegrationPR(context.Background(), spy, dir, it)
	if err != nil || url != "https://github.com/o/r/pull/9" || len(spy.specs) != 1 {
		t.Fatalf("open: %q %v %+v", url, err, spy.specs)
	}
	s := spy.specs[0]
	if s.Head != it.Branch || s.Base != "main" || !strings.Contains(s.Body, "Closes #5\n") || !strings.Contains(s.Body, "Closes #6\n") {
		t.Fatalf("PR spec: %+v", s)
	}
	reg, _ := worker.LoadRegistry(dir)
	rec, ok := reg.IntegrationByPR("9")
	if !ok || rec.Branch != it.Branch {
		t.Fatalf("PR not recorded: %+v %v", rec, ok)
	}
	// A rerun after a fix keeps the PR: it follows the branch.
	if again, err := openIntegrationPR(context.Background(), spy, dir, rec); err != nil || again != url || len(spy.specs) != 1 {
		t.Fatalf("rerun opened another PR: %q %v %d", again, err, len(spy.specs))
	}
}
