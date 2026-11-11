package worker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// integrationWorld is two open PRs, #7 (w1, work.txt) and #8 (w2, work2.txt),
// in round 55, both closing their issue: a train of them may land.
func integrationWorld(t *testing.T) *world {
	t.Helper()
	w := slicePRTrain(t)
	pr := func(n int) string { return fmt.Sprintf("https://github.com/o/r/pull/%d", n) }
	if err := os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(fmt.Sprintf(
		`{"round":55,"slots":[{"name":"w1","branch":"w1","task":"#5","pr":%q},{"name":"w2","branch":"w2","task":"#6","pr":%q}]}`, pr(7), pr(8))), 0o644); err != nil {
		t.Fatal(err)
	}
	w.setPRBody("Closes #5\nCloses #6\n")
	w.forge("issueLabels", "in-progress")
	return w
}

// remoteLog is `git log` of a ref in the bare origin.
func (w *world) remoteLog(args ...string) string {
	return gittest.Run(w.t, w.origin, append([]string{"log"}, args...)...)
}

func TestTrainIntegrationPushesBranch(t *testing.T) {
	w := integrationWorld(t)
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"})
	if err != nil || res.Verdict != GatePass || res.Integration == nil {
		t.Fatalf("integration train: %+v %v", res, err)
	}
	const branch = "integration/r55-a"
	if res.Integration.Branch != branch || len(res.Landed) != 0 || res.Changed {
		t.Fatalf("an integration train lands nothing on main: %+v", res)
	}
	if w.onMain("work.txt") || w.onMain("work2.txt") {
		t.Fatal("a member reached main")
	}
	// One --no-ff merge per member on the first-parent line, whose second
	// parent is that member's head.
	merges := strings.Fields(w.remoteLog("--merges", "--first-parent", "--format=%P", branch))
	heads := map[string]bool{w.forgeWord("sha"): true, w.forgeWord("sha#8"): true}
	got := map[string]bool{}
	for i := 1; i < len(merges); i += 2 {
		got[merges[i]] = true
	}
	if len(merges) != 4 || !got[w.forgeWord("sha")] || !got[w.forgeWord("sha#8")] {
		t.Fatalf("want one merge per member %v, got parents %v", heads, merges)
	}
	reg, err := LoadRegistry(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := reg.IntegrationByBranch(branch)
	tip := strings.TrimSpace(gittest.Run(t, w.origin, "rev-parse", branch))
	if !ok || rec.Name != "a" || rec.Round != 55 || len(rec.Members) != 2 || rec.Assembled != tip || rec.VerifiedTip != tip || len(rec.RotaMerges) != 2 {
		t.Fatalf("round state: %+v (ok %v), tip %s", rec, ok, tip)
	}
}

func TestTrainIntegrationRemergesMain(t *testing.T) {
	w := integrationWorld(t)
	if res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"}); err != nil || res.Verdict != GatePass {
		t.Fatalf("first run: %+v %v", res, err)
	}
	// main moves between two runs.
	trainWrite(t, w, "later.txt", "later")
	gittest.Run(t, w.dir, "push", "-q", "origin", "main")
	main := strings.TrimSpace(gittest.Run(t, w.dir, "rev-parse", "main"))
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("second run: %+v %v", res, err)
	}
	const branch = "integration/r55-a"
	if err := exec.Command("git", "-C", w.origin, "merge-base", "--is-ancestor", main, branch).Run(); err != nil {
		t.Fatalf("main %s is not in %s:\n%s", main, branch, w.remoteLog("--oneline", "--graph", branch))
	}
	// The members were already merged: the rerun merges only main.
	if n := len(strings.Fields(w.remoteLog("--merges", "--first-parent", "--format=%H", branch))); n != 3 {
		t.Fatalf("want two member merges and one main merge, got %d:\n%s", n, w.remoteLog("--first-parent", "--oneline", branch))
	}
	reg, _ := LoadRegistry(w.dir)
	rec, _ := reg.IntegrationByBranch(branch)
	if len(rec.RotaMerges) != 3 || rec.VerifiedBase != main {
		t.Fatalf("record after the rerun: %+v", rec)
	}
}

func TestTrainIntegrationNeedsPRs(t *testing.T) {
	w := trainWorld(t, "true", "b1")
	_, err := w.train(TrainOpts{Targets: []string{"b1"}, Integration: "a"})
	if err == nil || !strings.Contains(err.Error(), "PR") {
		t.Fatalf("a slot without a PR: %v", err)
	}
}

// clashW2 makes PR #8 also add work.txt, differently from #7.
func clashW2(t *testing.T, w *world) {
	t.Helper()
	os.WriteFile(filepath.Join(w.worker, "work.txt"), []byte("other\n"), 0o644)
	gittest.Run(t, w.worker, "add", "work.txt")
	gittest.Run(t, w.worker, "commit", "-q", "-m", "clash")
	gittest.Run(t, w.worker, "push", "-q", "origin", "w2")
	w.forge("sha#8", strings.TrimSpace(gittest.Run(t, w.worker, "rev-parse", "HEAD")))
}

func TestTrainIntegrationConflictPushesPrefix(t *testing.T) {
	w := integrationWorld(t)
	clashW2(t, w)
	const branch = "integration/r55-a"
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Order: []string{"7", "8"}, Integration: "a"})
	if err != nil || res.Verdict != GateInteraction || res.Culprit != "" || strings.Join(res.Interaction, ",") != "8,7" {
		t.Fatalf("a conflicting member is an interaction, not a culprit: %+v %v", res, err)
	}
	if n := len(strings.Fields(w.remoteLog("--merges", "--first-parent", "--format=%H", branch))); n != 1 {
		t.Fatalf("want the member before the conflict pushed, got %d merge(s)", n)
	}
	reg, _ := LoadRegistry(w.dir)
	rec, _ := reg.IntegrationByBranch(branch)
	m, ok := rec.Member("8")
	if !ok || !m.Pending || strings.Join(m.Conflicts, ",") != "work.txt" || rec.VerifiedTip != "" {
		t.Fatalf("pending member not recorded: %+v", rec)
	}
	// A worker resolves the conflict on the integration branch, not on w2.
	gittest.Run(t, w.worker, "fetch", "-q", "origin", branch)
	gittest.Run(t, w.worker, "checkout", "-q", "-b", "fix", "FETCH_HEAD")
	exec.Command("git", "-C", w.worker, "merge", "-q", "--no-ff", "w2", "-m", "resolve").Run()
	os.WriteFile(filepath.Join(w.worker, "work.txt"), []byte("work\nother\n"), 0o644)
	gittest.Run(t, w.worker, "add", "work.txt")
	gittest.Run(t, w.worker, "commit", "-q", "--no-edit")
	gittest.Run(t, w.worker, "push", "-q", "origin", "fix:"+branch)
	res, err = w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("rerun after the resolution: %+v %v", res, err)
	}
	reg, _ = LoadRegistry(w.dir)
	rec, _ = reg.IntegrationByBranch(branch)
	if m, _ := rec.Member("8"); m.Pending || rec.VerifiedTip == "" {
		t.Fatalf("the resolved member is still pending: %+v", rec)
	}
}

func TestTrainIntegrationInteractionKeepsBranch(t *testing.T) {
	w := integrationWorld(t)
	// Each member passes alone; the two together fail.
	w.setConfig(`{"test":{"full":["! test -f work.txt || ! test -f work2.txt"]}}`)
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"})
	if err != nil || res.Verdict != GateInteraction || res.Culprit != "" || len(res.Landed) != 0 {
		t.Fatalf("interaction: %+v %v", res, err)
	}
	const branch = "integration/r55-a"
	if n := len(strings.Fields(w.remoteLog("--merges", "--first-parent", "--format=%H", branch))); n != 2 {
		t.Fatalf("the red tip stays pushed to fix on, got %d merge(s)", n)
	}
	reg, _ := LoadRegistry(w.dir)
	if rec, _ := reg.IntegrationByBranch(branch); rec.VerifiedTip != "" || rec.Assembled == "" {
		t.Fatalf("a red tip is not verified: %+v", rec)
	}
}
