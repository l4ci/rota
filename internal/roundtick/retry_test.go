package roundtick

import (
	"context"
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

func TestTickFailedHeadWaitsForPushAcrossRestart(t *testing.T) {
	for _, v := range []string{worker.GateStale, worker.GateProvenanceFail, worker.GateVerifyFailed} {
		for _, train := range []bool{false, true} {
			name := "gate"
			if train {
				name = "train"
			}
			t.Run(v+"/"+name, func(t *testing.T) {
				f := &fake{slots: []Slot{{Name: "ben", State: "done", PR: "#10"}}}
				if train {
					f.queued = []string{"#11"}
				}
				f.gateOut = func(string) GateRun {
					return GateRun{Result: worker.GateResult{Verdict: v}}
				}
				f.trainOut = func([]string) TrainRun {
					return TrainRun{Result: worker.TrainResult{Verdict: v, Culprit: "ben"}}
				}
				head := "head-a"
				e := f.env()
				e.Head = func(context.Context, string) (string, error) { return head, nil }
				cd := t.TempDir()
				tick := func() {
					t.Helper()
					LoadState(cd, 40).Apply(&e)
					r, err := Run(context.Background(), e)
					if err != nil {
						t.Fatal(err)
					}
					if err := SaveState(cd, 40, r); err != nil {
						t.Fatal(err)
					}
				}
				tick()
				// The train's innocent member can run separately on later ticks.
				f.queued = nil
				f.slots[0].State = "busy"
				tick()
				f.slots[0].State = "done"
				tick()
				if got := len(f.gated) + len(f.trained); got != 1 {
					t.Fatalf("unchanged failed head attempted %d times, want 1", got)
				}
				head = "head-b"
				tick()
				if got := len(f.gated) + len(f.trained); got != 2 {
					t.Fatalf("new head attempted %d times total, want 2", got)
				}
			})
		}
	}
}

func TestTickUnreadableHeadDoesNotRetry(t *testing.T) {
	f := &fake{queued: []string{"#10"}}
	e := f.env()
	e.Head = func(context.Context, string) (string, error) { return "", errors.New("forge unavailable") }
	e.FailedHeads = map[string]FailedHead{"#10": {Head: "head-a", Why: "tests failed"}}
	r, err := Run(context.Background(), e)
	if err != nil || len(f.gated)+len(f.trained) != 0 || r.FailedHeads["#10"].Head != "head-a" {
		t.Fatalf("unreadable head retried or forgot failure: %+v %v", r, err)
	}
}

func TestTickQueuedFailureClearsOnNewHead(t *testing.T) {
	f := &fake{queued: []string{"#10"}}
	e := f.env()
	e.Head = func(context.Context, string) (string, error) { return "head-b", nil }
	e.Held = map[string]string{"#10": "verify-failed"}
	e.FailedHeads = map[string]FailedHead{"#10": {Head: "head-a", Why: "verify-failed"}}
	r, err := Run(context.Background(), e)
	if err != nil || len(f.gated) != 1 || len(r.FailedHeads) != 0 || len(r.Held) != 0 {
		t.Fatalf("new queued head did not clear failure: %+v %v", r, err)
	}
	if e.FailedHeads["#10"].Head != "head-a" {
		t.Fatal("tick mutated its input state")
	}
}
