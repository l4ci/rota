package worker

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/landfreeze"
)

// flakyOnceE2E fails the e2e command once on an assertion, then passes it.
func flakyOnceE2E(calls *int) func(context.Context, string, string) (string, int) {
	return func(_ context.Context, _, cmd string) (string, int) {
		if cmd != "e2e" {
			return "ok\n", 0
		}
		*calls++
		if *calls == 1 {
			return "--- FAIL: TestE2E (0s)\nFAIL\n", 1
		}
		return "ok\n", 0
	}
}

func TestFreezeRecoveryRetriesFlakyE2E(t *testing.T) {
	w, p := frozenWorld(t)
	w.setConfig(`{"test":{"full":["full"],"e2e":["e2e"]}}`)
	e := w.env(false)
	calls := 0
	e.Shell = flakyOnceE2E(&calls)
	r, err := e.Freeze(bg, w.dir, FreezeOpts{Verify: true})
	stored, rerr := landfreeze.Read(p)
	if err != nil || r.Active || calls != 2 || rerr != nil || stored.Active {
		t.Fatalf("flaky base e2e must recover once: %+v calls=%d err=%v", r, calls, err)
	}
	rows, err := flakeledger.Load(w.dir)
	if err != nil || len(rows) != 1 || rows[0].Check != "e2e" {
		t.Fatalf("recovery must reach the flake ledger: %+v %v", rows, err)
	}
}

func TestLandedTrainRetriesFlakyE2E(t *testing.T) {
	w := trainWorld(t, "full", "b1")
	w.setConfig(`{"test":{"full":["full"],"e2e":["e2e"]}}`)
	e := w.env(false)
	calls := 0
	e.Shell = flakyOnceE2E(&calls)
	e = e.WithDefaults()
	in, err := e.GateInput(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	tr := &trainRun{e: e, ctx: bg, root: w.dir, in: in, verify: func(dir string) (VerifyResult, error) {
		return e.RunVerify(bg, in.cfg.Full, dir)
	}}
	res := TrainResult{}
	stop, err := tr.verifyLanded(&res)
	if err != nil || stop || calls != 2 {
		t.Fatalf("flaky post-landing e2e must recover once: stop=%v calls=%d err=%v res=%+v", stop, calls, err, res)
	}
	rows, err := flakeledger.Load(w.dir)
	if err != nil || len(rows) != 1 || rows[0].Check != "e2e" {
		t.Fatalf("recovery must reach the flake ledger: %+v %v", rows, err)
	}
}
