package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// A slot holding an attempt of a best-of:2 issue no pick names is refused by
// `worker gate` and `worker train` with exit 4, blockedBy best-of-unpicked.
func TestWorkerGateAndTrainRefuseAnUnpickedBestOf(t *testing.T) {
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
	for _, args := range [][]string{
		{"worker", "gate", "w1", "--base", "main"},
		{"worker", "gate", "w1", "--base", "main", "--check-only"},
		{"worker", "train", "w1", "--base", "main"},
	} {
		code, out, _ := rotaIn(t, dir, append([]string{"--json"}, args...)...)
		d := data(t, out)
		if code != 4 || d["blockedBy"] != "best-of-unpicked" || d["verdict"] != "best-of-unpicked" || d["changed"] != false {
			t.Fatalf("%v: %d %v", args, code, d)
		}
		if _, err := os.Stat(filepath.Join(dir, "feature.txt")); err == nil {
			t.Fatalf("%v merged an unpicked attempt", args)
		}
	}
}
