package cli

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/worker"
)

func TestGatePassEndsTheItemClock(t *testing.T) {
	root := t.TempDir()
	if err := worker.RecordItemStart(root, "12", time.Now()); err != nil {
		t.Fatal(err)
	}
	// A stale verdict is a bounce, not a landing: the clock keeps running.
	if _, _, err := gateBounce(nil, root, "12", worker.GateResult{Verdict: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := worker.LoadRegistry(root).ItemStart("12"); !ok {
		t.Fatal("a non-pass verdict must keep the clock")
	}
	if _, _, err := gateBounce(nil, root, "12", worker.GateResult{Verdict: worker.GatePass}); err != nil {
		t.Fatal(err)
	}
	if _, ok := worker.LoadRegistry(root).ItemStart("12"); ok {
		t.Error("a gate pass must clear the item start")
	}
}
