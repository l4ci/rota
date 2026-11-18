package worker

import (
	"context"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/postgate"
)

func TestLandingAdapterEndsLandedItem(t *testing.T) {
	w := newWorld(t, "")
	// A local branch is enough to exercise the real gate and registry bookkeeping.
	if _, err := UpdateSlot(w.dir, "w1", func(s *Slot) { s.SetTask("12") }); err != nil {
		t.Fatal(err)
	}
	if err := RecordItemStart(w.dir, "12", time.Now()); err != nil {
		t.Fatal(err)
	}
	var released string
	env := w.env(false).Landing(func(_, issue, slot string) error { released = issue + "@" + slot; return nil })
	run := env.Run(context.Background(), w.dir, postgate.Request{Gate: &postgate.GateOpts{Slot: "w1", Base: "main"}}).Gate
	if run.Err != nil || run.BookErr != nil || run.Refused || run.Result.Verdict != GatePass {
		t.Fatalf("run: %+v", run)
	}
	if _, ok := LoadRegistryTolerant(w.dir).ItemStart("12"); ok {
		t.Fatal("landed item clock remains")
	}
	if released != "12@w1" {
		t.Fatalf("released %q, want 12@w1", released)
	}
}
