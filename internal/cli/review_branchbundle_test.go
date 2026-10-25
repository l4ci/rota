package cli

import (
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

func TestBranchBundleFallsBackToQueuedWhenSlotBundleEmpty(t *testing.T) {
	root := t.TempDir()
	const branch = "kit/7-x"
	if err := worker.Update(root, func(d *worker.Doc) {
		d.AppendSlot(worker.NewSlot("kit", branch, "/wt/kit", "main", ""))
		d.QueuePR(worker.QueuedPR{Issue: "#7", Branch: branch, PR: "#70", Bundle: []string{"#8"}})
	}); err != nil {
		t.Fatal(err)
	}
	got := branchBundle(&Ctx{}, root, branch)
	if len(got) != 1 || got[0].String() != "8" {
		t.Fatalf("bundle = %v, want [8]", got)
	}
}
