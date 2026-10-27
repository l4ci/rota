package roundtick

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

func TestTickDefersBusyLanding(t *testing.T) {
	for _, train := range []bool{false, true} {
		name := "gate"
		if train {
			name = "train"
		}
		t.Run(name, func(t *testing.T) {
			f := &fake{slots: []Slot{{Name: "ben", State: "done", PR: "#11"}}}
			if train {
				f.slots = append(f.slots, Slot{Name: "dana", State: "done", PR: "#12"})
			}
			f.gateOut = func(string) GateRun {
				return GateRun{Result: worker.GateResult{Verdict: worker.GateRunning, Err: "manual landing is running or queued"}, Refused: true}
			}
			f.trainOut = func([]string) TrainRun {
				return TrainRun{Result: worker.TrainResult{Verdict: worker.GateRunning, Err: "manual landing is running or queued"}, Refused: true}
			}
			r, err := Run(context.Background(), f.env())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(r.Deferred, "manual landing is running or queued") {
				t.Fatalf("Deferred = %q, want the busy landing reason", r.Deferred)
			}
			if len(r.Did) != 0 || len(r.NeedsYou) != 0 || len(r.Held) != 0 || len(r.FailedHeads) != 0 {
				t.Fatalf("a busy landing must be retried without a failure or hold: %+v", r)
			}
		})
	}
}
