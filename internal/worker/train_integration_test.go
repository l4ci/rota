package worker

import (
	"fmt"
	"github.com/l4ci/rota/internal/landing"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	if err != nil || res.Verdict != landing.GatePass || res.Integration == nil {
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
	if res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"}); err != nil || res.Verdict != landing.GatePass {
		t.Fatalf("first run: %+v %v", res, err)
	}
	// main moves between two runs.
	trainWrite(t, w, "later.txt", "later")
	gittest.Run(t, w.dir, "push", "-q", "origin", "main")
	main := strings.TrimSpace(gittest.Run(t, w.dir, "rev-parse", "main"))
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"})
	if err != nil || res.Verdict != landing.GatePass {
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
	if err := os.WriteFile(filepath.Join(w.worker, "work.txt"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	if err != nil || res.Verdict != landing.GateInteraction || res.Culprit != "" || strings.Join(res.Interaction, ",") != "8,7" {
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
	// The merge must stop on the clash: the resolution below finishes it.
	if err := exec.Command("git", "-C", w.worker, "merge", "-q", "--no-ff", "w2", "-m", "resolve").Run(); err == nil {
		t.Fatal("merging w2 onto the integration branch should conflict")
	}
	if err := os.WriteFile(filepath.Join(w.worker, "work.txt"), []byte("work\nother\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, w.worker, "add", "work.txt")
	gittest.Run(t, w.worker, "commit", "-q", "--no-edit")
	gittest.Run(t, w.worker, "push", "-q", "origin", "fix:"+branch)
	res, err = w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a"})
	if err != nil || res.Verdict != landing.GatePass {
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
	if err != nil || res.Verdict != landing.GateInteraction || res.Culprit != "" || len(res.Landed) != 0 {
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

// redWork2Full is a test.full that counts its runs in count and fails one
// test once the second member's file is merged in.
func redWork2Full(t *testing.T, w *world) string {
	t.Helper()
	count := filepath.Join(t.TempDir(), "count")
	w.setConfig(`{"test":{"full":["echo x >> ` + count + `; if test -f work2.txt; then echo '--- FAIL: TestWidget (0.00s)'; echo FAIL; exit 1; fi"]}}`)
	return count
}

// --on-fail stop is accepted with --integration (#3687): a red verify names
// the failed tests and ends, with no prefix verify and no culprit.
func TestTrainIntegrationOnFailStopSkipsDiagnose(t *testing.T) {
	w := integrationWorld(t)
	redWork2Full(t, w)
	w.forge("head#7", "w1")
	w.forge("sha#7", w.forgeWord("sha"))
	var said []string
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a", OnFail: OnFailStop, Say: func(s string) { said = append(said, s) }})
	if err != nil || res.Verdict != landing.GateVerifyFailed || res.Stop == nil {
		t.Fatalf("on-fail stop with --integration: %+v %v", res, err)
	}
	if strings.Join(res.Stop.Tests, ",") != "TestWidget" || !strings.Contains(res.Err, "failing tests: TestWidget") {
		t.Errorf("stop tests %v, text:\n%s", res.Stop.Tests, res.Err)
	}
	// The verify may re-run its own failed test; the train adds no re-run, no
	// prefix verify and no estimate.
	for _, l := range said {
		if strings.Contains(l, "diagnose") || strings.Contains(l, "rerun") || strings.Contains(l, "classify") {
			t.Errorf("stop must diagnose nothing, said %q", l)
		}
	}
	if res.Culprit != "" || len(res.Diagnostic) != 0 {
		t.Errorf("stop blames nobody: %+v", res)
	}
}

// Without --on-fail stop a red integration verify prints the failed tests
// and the diagnose budget before any diagnose run starts (#3687).
func TestTrainIntegrationRedPrintsFailuresBeforeDiagnose(t *testing.T) {
	w := integrationWorld(t)
	redWork2Full(t, w)
	w.forge("head#7", "w1")
	w.forge("sha#7", w.forgeWord("sha"))
	var said []string
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a", Say: func(s string) { said = append(said, s) }})
	if err != nil || res.Verdict != landing.GateVerifyFailed {
		t.Fatalf("red integration train: %+v %v", res, err)
	}
	at := func(sub string) int {
		return slices.IndexFunc(said, func(l string) bool { return strings.Contains(l, sub) })
	}
	failed, budget, diagnose := at("verify failed: TestWidget"), at("diagnose estimate: up to"), at("PHASE diagnose ")
	if failed < 0 || budget < 0 || diagnose < 0 {
		t.Fatalf("want failed-test, budget and diagnose lines, got %d %d %d in %q", failed, budget, diagnose, said)
	}
	if failed > budget || budget > diagnose {
		t.Errorf("order: failed tests %d, budget %d, first diagnose run %d: %q", failed, budget, diagnose, said)
	}
}

// --no-verify builds and pushes the branch without running test.full, even
// when one is configured (#3683); the gate then verifies the tip itself and
// refuses it when that verify is red.
func TestTrainIntegrationNoVerifySkipsConfiguredVerify(t *testing.T) {
	w := integrationWorld(t)
	count := redWork2Full(t, w)
	w.forge("head#7", "w1")
	w.forge("sha#7", w.forgeWord("sha"))
	res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Integration: "a", NoVerify: true})
	if err != nil || res.Verdict != landing.GatePass {
		t.Fatalf("no-verify integration train: %+v %v", res, err)
	}
	if n := intRuns(count); n != 0 {
		t.Errorf("--no-verify ran test.full %d time(s)", n)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "INTEGRATION-UNVERIFIED") {
		t.Errorf("notes: %v", res.Notes)
	}
	if rec := intRecord(t, w); rec.VerifiedTip != "" || rec.Assembled == "" {
		t.Fatalf("record: %+v", rec)
	}
	if err := UpdateIntegration(w.dir, intBranch, func(it *landing.Integration) { it.PR = "https://github.com/o/r/pull/9" }); err != nil {
		t.Fatal(err)
	}
	reg, _ := LoadRegistry(w.dir)
	rec, _ := reg.IntegrationByBranch(intBranch)
	w.forge("body#9", IntegrationPRBody(rec))
	w.pointPR9()
	g, err := w.landPR9()
	if err != nil || g.Verdict != landing.GateVerifyFailed {
		t.Fatalf("the gate landed an unverified tip whose verify is red: %+v %v", g, err)
	}
	if intRuns(count) == 0 {
		t.Errorf("the gate must verify the unverified tip itself, runs %d", intRuns(count))
	}
	if w.onMain("work.txt") || w.onMain("work2.txt") {
		t.Fatal("an unverified tip reached main")
	}
}

// A modify/delete conflict names the merged member it collides with, also for
// a name git would quote.
func TestTrainIntegrationModifyDeleteNamesTheCollidingMember(t *testing.T) {
	for _, name := range []string{"seed.txt", "sp ü.txt"} {
		t.Run(name, func(t *testing.T) {
			w := integrationWorld(t)
			if name != "seed.txt" {
				trainWrite(t, w, name, "base")
				gittest.Run(t, w.dir, "push", "-q", "origin", "main")
				gittest.Run(t, w.worker, "fetch", "-q", "origin")
			}
			// #7 modifies the file, #8 deletes it.
			for _, m := range []struct{ branch, key string }{{"w1", "sha"}, {"w2", "sha#8"}} {
				gittest.Run(t, w.worker, "checkout", "-q", m.branch)
				if name != "seed.txt" {
					gittest.Run(t, w.worker, "merge", "-q", "--no-edit", "origin/main")
				}
				if m.branch == "w1" {
					gittest.Commit(t, w.worker, "modify", name, "changed\n")
				} else {
					gittest.Run(t, w.worker, "rm", "-q", name)
					gittest.Run(t, w.worker, "commit", "-q", "-m", "delete")
				}
				gittest.Run(t, w.worker, "push", "-q", "origin", m.branch)
				w.forge(m.key, gittest.Run(t, w.worker, "rev-parse", "HEAD"))
			}
			res, err := w.train(TrainOpts{Targets: []string{"7", "8"}, Order: []string{"7", "8"}, Integration: "a"})
			if err != nil || res.Verdict != landing.GateInteraction || strings.Join(res.Interaction, ",") != "8,7" {
				t.Fatalf("want 8 colliding with 7: %q %+v %v", res.Interaction, res, err)
			}
			reg, _ := LoadRegistry(w.dir)
			rec, _ := reg.IntegrationByBranch("integration/r55-a")
			if m, ok := rec.Member("8"); !ok || len(m.Conflicts) != 1 || m.Conflicts[0] != name {
				t.Fatalf("conflicts = %q, want %q", m.Conflicts, name)
			}
		})
	}
}
