package worker

import (
	"context"
	"errors"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/landing"
	"os"
	"path/filepath"
	"strings"
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
	}{{"green", 0, landing.GatePass}, {"red", 1, landing.GateVerifyFailed}} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, "")
			if err := os.Remove(filepath.Join(w.dir, ".rota", "config.json")); err != nil {
				t.Fatal(err)
			}
			gittest.Run(t, w.dir, "fetch", "-q", "origin", "w1:w1")
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

// A malformed test.full stops the gate and the train, naming the key as
// malformed, rather than reading as empty and blaming a key that is set.
func TestGateAndTrainRefuseMalformedTestFull(t *testing.T) {
	w := newWorld(t, "")
	w.setConfig(`{"test":{"full":["F",1]}}`)
	e := w.env(false)
	check := func(who string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "test.full must be a list of strings") {
			t.Errorf("%s err = %v, want test.full malformed", who, err)
		}
	}
	_, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	check("gate", err)
	_, err = e.Train(bg, w.dir, TrainOpts{Targets: []string{"w1"}, Base: "main"})
	check("train", err)
}

// A train reads the gate input once, before any member lands. Member b1 edits
// .rota/config.json, so a later member's gate that re-read root would verify and
// land under the config b1 brought, not the one the train started with.
func TestTrainLoadsGateInputOnce(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	cfgPath := filepath.Join(".rota", "config.json")
	gittest.Run(t, w.dir, "add", "-f", cfgPath)
	gittest.Run(t, w.dir, "commit", "-q", "-m", "track config")
	for _, b := range []string{"b1", "b2"} {
		gittest.Run(t, w.dir, "checkout", "-q", b)
		gittest.Run(t, w.dir, "merge", "-q", "main")
	}
	gittest.Run(t, w.dir, "checkout", "-q", "b1")
	const edited = `{"issues":{"source":"github"},"test":{"full":["false"]}}`
	if err := os.WriteFile(filepath.Join(w.dir, cfgPath), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, w.dir, "commit", "-q", "-am", "b1 edits the config")
	gittest.Run(t, w.dir, "checkout", "-q", "main")

	e := w.env(false)
	load := e.loadGateInput
	var seen [][]string
	e.GateInput = func(root string) (gateInput, error) {
		in, err := load(root)
		seen = append(seen, in.cfg.Full)
		return in, err
	}
	res, err := e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != landing.GatePass || strings.Join(res.Landed, ",") != "b1,b2" {
		t.Fatalf("%+v %v", res, err)
	}
	if got, _ := os.ReadFile(filepath.Join(w.dir, cfgPath)); string(got) != edited {
		t.Fatalf("b1 did not land its config edit: %s", got)
	}
	if len(seen) != 1 || len(seen[0]) != 1 || seen[0][0] != "true" {
		t.Errorf("GateInput loads = %v, want one load of the original test.full [true]", seen)
	}
}
