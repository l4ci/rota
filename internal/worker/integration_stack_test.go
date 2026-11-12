package worker

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

const intBranchB = "integration/r55-b"

// stackedWorld is integration a (member #7, PR #9 to main) and integration b
// (member #8) stacked on it with `--onto a`, its PR #10 recorded.
func stackedWorld(t *testing.T) *world {
	t.Helper()
	w := integrationWorld(t)
	integrationPRFor(t, w, []string{"7"})
	res, err := w.train(TrainOpts{Targets: []string{"8"}, Integration: "b", Onto: "a"})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("stacked train: %+v %v", res, err)
	}
	if err := UpdateIntegration(w.dir, intBranchB, func(it *Integration) { it.PR = "https://github.com/o/r/pull/10" }); err != nil {
		t.Fatal(err)
	}
	return w
}

// integrationPRFor is integrationPR for a train of just targets.
func integrationPRFor(t *testing.T, w *world, targets []string) {
	t.Helper()
	w.forge("head#7", "w1")
	w.forge("sha#7", w.forgeWord("sha"))
	if res, err := w.train(TrainOpts{Targets: targets, Integration: "a"}); err != nil || res.Verdict != GatePass {
		t.Fatalf("integration train: %+v %v", res, err)
	}
	if err := UpdateIntegration(w.dir, intBranch, func(it *Integration) { it.PR = "https://github.com/o/r/pull/9" }); err != nil {
		t.Fatal(err)
	}
	reg, _ := LoadRegistry(w.dir)
	rec, _ := reg.IntegrationByBranch(intBranch)
	w.forge("body#9", IntegrationPRBody(rec))
	w.pointPR9()
}

// pointPR10 makes PR #10 (branch b) the forge's primary PR, open on main.
func (w *world) pointPR10() string {
	tip := strings.TrimSpace(gittest.Run(w.t, w.origin, "rev-parse", intBranchB))
	for k, v := range map[string]string{"head": intBranchB, "sha": tip, "head#10": intBranchB, "sha#10": tip, "state": "OPEN", "base": "main"} {
		w.forge(k, v)
	}
	reg, _ := LoadRegistry(w.dir)
	rec, _ := reg.IntegrationByBranch(intBranchB)
	w.forge("body#10", IntegrationPRBody(rec))
	return tip
}

func (w *world) landPR10() (GateResult, error) {
	return w.env(false).Gate(bg, w.dir, GateOpts{Slot: "10", Base: "main"})
}

func TestTrainOntoStacksOnOpenIntegration(t *testing.T) {
	w := stackedWorld(t)
	reg, _ := LoadRegistry(w.dir)
	a, _ := reg.IntegrationByBranch(intBranch)
	b, ok := reg.IntegrationByBranch(intBranchB)
	if !ok || b.Base != intBranch || b.Onto != intBranch || b.OntoBase != "main" {
		t.Fatalf("b must be based on a's branch: %+v (ok %v)", b, ok)
	}
	if err := exec.Command("git", "-C", w.origin, "merge-base", "--is-ancestor", a.Assembled, intBranchB).Run(); err != nil {
		t.Fatalf("b is not built on a's tip %s:\n%s", a.Assembled, w.remoteLog("--oneline", "--graph", intBranchB))
	}
	if w.onMain("work.txt") || w.onMain("work2.txt") {
		t.Fatal("a member reached main")
	}
	if got := IntegrationPRBody(b); !strings.Contains(got, "`"+intBranch+"`") {
		t.Errorf("the PR body names no base:\n%s", got)
	}
}

func TestTrainOntoRefusals(t *testing.T) {
	w := integrationWorld(t)
	for name, c := range map[string]struct {
		o    TrainOpts
		want string
	}{
		"without --integration": {TrainOpts{Targets: []string{"8"}, Onto: "a"}, "--onto needs --integration"},
		"onto itself":           {TrainOpts{Targets: []string{"8"}, Integration: "a", Onto: "a"}, "cannot stack on itself"},
		"onto an unknown one":   {TrainOpts{Targets: []string{"8"}, Integration: "b", Onto: "zz"}, "is not an open integration"},
		"a bad name":            {TrainOpts{Targets: []string{"8"}, Integration: "b", Onto: "a/b"}, "not a valid"},
	} {
		if _, err := w.train(c.o); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", name, err, c.want)
		}
	}
	integrationPRFor(t, w, []string{"7"})
	if err := UpdateIntegration(w.dir, intBranch, func(it *Integration) { it.Landed = true }); err != nil {
		t.Fatal(err)
	}
	if _, err := w.train(TrainOpts{Targets: []string{"8"}, Integration: "b", Onto: "a"}); err == nil || !strings.Contains(err.Error(), "already landed") {
		t.Errorf("onto a landed integration: %v", err)
	}
}

func TestStackedLandingRetargetsAndSkipsVerify(t *testing.T) {
	w := stackedWorld(t)
	count := countingFull(t, w)
	if res, err := w.landPR9(); err != nil || res.Verdict != GatePass {
		t.Fatalf("landing a: %+v %v", res, err)
	}
	before := intRuns(count)
	if !strings.Contains(w.logText(), "PRSetBase 10 main") {
		t.Fatalf("PR #10 was not retargeted to main:\n%s", w.logText())
	}
	reg, _ := LoadRegistry(w.dir)
	b, _ := reg.IntegrationByBranch(intBranchB)
	if b.Base != "main" || b.Onto != "" {
		t.Fatalf("b still stacked: %+v", b)
	}
	main := strings.TrimSpace(gittest.Run(t, w.origin, "rev-parse", "main"))
	if err := exec.Command("git", "-C", w.origin, "merge-base", "--is-ancestor", main, intBranchB).Run(); err != nil {
		t.Fatalf("main is not merged into b:\n%s", w.remoteLog("--oneline", "--graph", intBranchB))
	}
	w.pointPR10()
	res, err := w.landPR10()
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("landing b: %+v %v", res, err)
	}
	if intRuns(count) != before || !strings.Contains(strings.Join(res.Notes, "\n"), "INTEGRATION-VERIFIED") {
		t.Fatalf("the merge changed no tree, yet b verified again (%d -> %d): %v", before, intRuns(count), res.Notes)
	}
	gittest.Run(t, w.dir, "pull", "-q", "origin", "main")
	if !w.onMain("work.txt") || !w.onMain("work2.txt") {
		t.Fatal("a and b are not both on main")
	}
}

func TestStackedLandingReverifiesWhenMergeChangedTree(t *testing.T) {
	w := stackedWorld(t)
	count := countingFull(t, w)
	trainWrite(t, w, "later.txt", "later")
	gittest.Run(t, w.dir, "push", "-q", "origin", "main")
	if res, err := w.landPR9(); err != nil || res.Verdict != GatePass {
		t.Fatalf("landing a: %+v %v", res, err)
	}
	before := intRuns(count)
	w.pointPR10()
	res, err := w.landPR10()
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("landing b: %+v %v", res, err)
	}
	if intRuns(count) <= before || !strings.Contains(strings.Join(res.Notes, "\n"), "INTEGRATION-REVERIFY") {
		t.Fatalf("main's change reached b's tree, yet it did not verify again (%d -> %d): %v", before, intRuns(count), res.Notes)
	}
}

func TestStackedRefusesWhileBaseIsOpen(t *testing.T) {
	w := stackedWorld(t)
	w.pointPR10()
	res, err := w.landPR10()
	if err != nil || res.Verdict != GateStale || !strings.Contains(res.Err, intBranch) || !strings.Contains(res.Err, "has not landed") {
		t.Fatalf("b must wait for a: %+v %v", res, err)
	}
	if w.onMain("work2.txt") {
		t.Fatal("b landed on main")
	}
}

func TestStackedRefusesWhenBaseClosedUnlanded(t *testing.T) {
	w := stackedWorld(t)
	w.pointPR10()
	w.forge("state#9", "CLOSED")
	res, err := w.landPR10()
	if err != nil || res.Verdict != GateStale || !strings.Contains(res.Err, intBranch) || !strings.Contains(res.Err, "closed without landing") {
		t.Fatalf("b must refuse and name a: %+v %v", res, err)
	}
	if w.onMain("work2.txt") {
		t.Fatal("b landed on main")
	}
}

func TestStackedRetargetRetriesAtLanding(t *testing.T) {
	w := stackedWorld(t)
	w.forge("setBaseErr", "forge down")
	res, err := w.landPR9()
	if err != nil || res.Verdict != GatePass || !strings.Contains(strings.Join(res.Notes, "\n"), "STACK-RETARGET "+intBranchB+" pending") {
		t.Fatalf("a landing must survive a retarget failure and say so: %+v %v", res, err)
	}
	w.forge("setBaseErr", "")
	w.pointPR10()
	if res, err := w.landPR10(); err != nil || res.Verdict != GatePass {
		t.Fatalf("landing b retries the retarget: %+v %v", res, err)
	}
	if !strings.Contains(w.logText(), "PRSetBase 10 main") {
		t.Fatalf("the retry never retargeted:\n%s", w.logText())
	}
}
