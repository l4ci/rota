package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// memInput is a gateInput with no config, ledger or verify files behind it.
func memInput(verify ...string) func(string) (gateInput, error) {
	return func(root string) (gateInput, error) {
		return gateInput{cfg: GateConfig{where: WhereLocal, Full: verify}}, nil
	}
}

// The gate verifies with the commands Env.GateInput hands it, not with the
// .rota/config.json on disk (removed here: no config, no ledger file).
func TestGateTakesItsInputFromTheEnvSeam(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    int
		verdict string
	}{{"green", 0, GatePass}, {"red", 1, GateVerifyFailed}} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, "")
			if err := os.Remove(filepath.Join(w.dir, ".rota", "config.json")); err != nil {
				t.Fatal(err)
			}
			gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
			var ran []string
			e := w.env(false)
			e.GateInput = memInput("injected-check")
			e.Shell = func(_ context.Context, _, c string) (string, int) { ran = append(ran, c); return "", tc.code }
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if err != nil {
				t.Fatal(err)
			}
			if res.Verdict != tc.verdict {
				t.Errorf("verdict = %s (%s), want %s", res.Verdict, res.Err, tc.verdict)
			}
			if len(ran) != 1 || ran[0] != "injected-check" {
				t.Errorf("shell ran %v, want [injected-check]", ran)
			}
		})
	}
}

// A failing loader stops the gate and the train before they touch the tree.
func TestGateAndTrainStopOnInputError(t *testing.T) {
	w := newWorld(t, "")
	boom := errors.New("boom")
	e := w.env(false)
	e.GateInput = func(string) (gateInput, error) { return gateInput{}, boom }
	if _, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"}); !errors.Is(err, boom) {
		t.Errorf("gate err = %v, want boom", err)
	}
	if _, err := e.Train(bg, w.dir, TrainOpts{Targets: []string{"w1"}, Base: "main"}); !errors.Is(err, boom) {
		t.Errorf("train err = %v, want boom", err)
	}
}
