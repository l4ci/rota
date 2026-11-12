package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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
		{"a name with a dot", []string{"--integration", "x.lock"}, "not a valid branch name"},
		{"--onto itself", []string{"--onto", "a"}, "cannot stack on itself"},
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
	if err := os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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

type prSpy struct {
	specs  []tracker.PRSpec
	bodies map[int]string
}

func (*prSpy) Provider() string  { return "github" }
func (*prSpy) PRNeedsBase() bool { return true }
func (p *prSpy) PRCreate(_ context.Context, s tracker.PRSpec) (string, error) {
	p.specs = append(p.specs, s)
	return "https://github.com/o/r/pull/9\n", nil
}
func (p *prSpy) PRSetBody(_ context.Context, pr int, body string) error {
	if p.bodies == nil {
		p.bodies = map[int]string{}
	}
	p.bodies[pr] = body
	return nil
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
	if !strings.Contains(s.Body, "## Assumptions") || !strings.Contains(s.Body, "## Rulings") {
		t.Fatalf("the gate refuses a body without the decision sections: %s", s.Body)
	}
	// A rerun keeps the PR (it follows the branch) but writes the body again,
	// so a member the rerun added gets its Closes line.
	rec.Members = append(rec.Members, worker.IntegrationMember{Target: "10", PR: "https://github.com/o/r/pull/10", Issue: "12"})
	if err := worker.RecordIntegration(dir, rec); err != nil {
		t.Fatal(err)
	}
	if again, err := openIntegrationPR(context.Background(), spy, dir, rec); err != nil || again != url || len(spy.specs) != 1 {
		t.Fatalf("rerun opened another PR: %q %v %d", again, err, len(spy.specs))
	}
	if !strings.Contains(spy.bodies[9], "Closes #12\n") || !strings.Contains(spy.bodies[9], "Closes #5\n") {
		t.Fatalf("rerun body: %q", spy.bodies[9])
	}
}

func TestWorkerTrainOntoNeedsIntegration(t *testing.T) {
	dir := planProject(t, gateConfig(t, `{"test":{"full":["true"]}}`, "off", nil), false)
	code, out, errOut := rotaIn(t, dir, "worker", "train", "w1", "--base", "main", "--onto", "a", "--no-assign-check")
	if code != 2 || !strings.Contains(out+errOut, "--onto needs --integration") {
		t.Fatalf("exit %d, want 2:\n%s%s", code, out, errOut)
	}
}

// A stacked integration branch opens its PR against the branch under it, and
// its rerun command carries --onto and the base it lands on afterwards.
func TestWorkerTrainStackedPRTargetsBranchUnder(t *testing.T) {
	dir := workerProject(t, `{"test":{"full":["true"]}}`)
	it := worker.Integration{Name: "b", Branch: "integration/r55-b", Round: 55, Base: "integration/r55-a", Onto: "integration/r55-a", OntoBase: "main",
		Members: []worker.IntegrationMember{{Target: "8", PR: "https://github.com/o/r/pull/8", Issue: "6"}}}
	if err := worker.RecordIntegration(dir, it); err != nil {
		t.Fatal(err)
	}
	spy := &prSpy{}
	if _, err := openIntegrationPR(context.Background(), spy, dir, it); err != nil || len(spy.specs) != 1 || spy.specs[0].Base != "integration/r55-a" {
		t.Fatalf("PR must target the branch under it: %v %+v", err, spy.specs)
	}
	if got, want := integrationRerun(it, []string{"8"}), "`rota worker train 8 --integration b --onto a --base main`"; got != want {
		t.Errorf("rerun %q, want %q", got, want)
	}
}

// A train that finished A+B and writes the PR body after another train added C
// must publish what the record holds now, not the A+B it saw: the body is
// written from a record read at publish time.
func TestOpenIntegrationPRPublishesFreshRecord(t *testing.T) {
	dir := workerProject(t, `{"test":{"full":["true"]}}`)
	pr := func(n string) string { return "https://github.com/o/r/pull/" + n }
	stale := worker.Integration{Name: "a", Branch: "integration/r55-a", Round: 55, Base: "main", PR: pr("9"),
		Members: []worker.IntegrationMember{{Target: "7", PR: pr("7"), Issue: "5"}, {Target: "8", PR: pr("8"), Issue: "6"}}}
	if err := worker.RecordIntegration(dir, stale); err != nil {
		t.Fatal(err)
	}
	fresh := stale
	fresh.Members = append(slices.Clone(stale.Members), worker.IntegrationMember{Target: "10", PR: pr("10"), Issue: "12"})
	if err := worker.RecordIntegration(dir, fresh); err != nil {
		t.Fatal(err)
	}
	spy := &prSpy{}
	if _, err := openIntegrationPR(context.Background(), spy, dir, stale); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(spy.bodies[9], "Closes #12\n") {
		t.Fatalf("the late member's Closes line was overwritten: %q", spy.bodies[9])
	}
}
