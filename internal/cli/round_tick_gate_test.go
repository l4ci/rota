package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// The autopilot's gate outcome comes from the gate run itself, not from a
// verb's JSON: a refused slot reports its verdict and why, and a bounced one
// is counted by the shared bookkeeping.
func TestTickGateOutcomeComesFromTheGateRun(t *testing.T) {
	dir := workerProject(t, `{"test":{"full":["test -f feature.txt"]}}`)
	rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	err := worker.Update(dir, func(d *worker.Doc) {
		d.Slot("w1").SetTask("#5")
		d.SetBestOf(worker.BestOf{Issue: "5", Round: 1, Attempts: []worker.BestOfAttempt{{Slot: "w1"}, {Slot: "w2"}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(dir, ".worktrees", "w1")
	write(t, filepath.Join(wt, "feature.txt"), "f")
	gitT(t, wt, "add", "feature.txt")
	gitT(t, wt, "commit", "-q", "-m", "feature")

	c := &Ctx{Deps: testDeps(), ctx: context.Background(), Stderr: io.Discard}
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	out := gateTarget(c, dir, "w1", "main")
	if out.Landed || out.Verdict != worker.GateBestOfUnpicked || out.Detail == "" {
		t.Fatalf("gate outcome: %+v", out)
	}
	tr := trainTargets(c, dir, []string{"w1"}, "main")
	if tr.Done || tr.Verdict != worker.GateBestOfUnpicked || len(tr.Landed) != 0 {
		t.Fatalf("train outcome: %+v", tr)
	}
}
