package worker

import (
	"bytes"
	"context"
	"github.com/l4ci/rota/internal/landing"
	"os"
	"strings"
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
	tr := &trainRun{e: e, ctx: bg, root: w.dir}
	p := trainPlan{in: in, verify: func(dir string) (VerifyResult, error) {
		return e.RunVerify(bg, in.cfg.Full, dir)
	}}
	res := landing.TrainResult{}
	stop, err := tr.verifyLanded(p, &res)
	if err != nil || stop || calls != 2 {
		t.Fatalf("flaky post-landing e2e must recover once: stop=%v calls=%d err=%v res=%+v", stop, calls, err, res)
	}
	rows, err := flakeledger.Load(w.dir)
	if err != nil || len(rows) != 1 || rows[0].Check != "e2e" {
		t.Fatalf("recovery must reach the flake ledger: %+v %v", rows, err)
	}
}

func TestFreezeRecoveryFlakeLedgerWriteFailureWarns(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "recovered"}[recovered], func(t *testing.T) {
			w, p := frozenWorld(t)
			w.setConfig(`{"test":{"full":["full"],"e2e":["e2e"]}}`)
			if err := os.Mkdir(flakeledger.Path(w.dir), 0o755); err != nil {
				t.Fatal(err)
			}
			e := w.env(false)
			var stderr bytes.Buffer
			e.Stderr = &stderr
			calls := 0
			e.Shell = func(_ context.Context, _, cmd string) (string, int) {
				if cmd == "e2e" {
					calls++
					if recovered && calls == 1 {
						return "--- FAIL: TestE2E (0s)\nFAIL\n", 1
					}
				}
				return "ok\n", 0
			}
			r, err := e.Freeze(bg, w.dir, FreezeOpts{Verify: true})
			if err != nil || r == nil || r.Active || r.ClearedSHA == "" {
				t.Fatalf("ledger failure must not prevent green recovery: %+v %v", r, err)
			}
			stored, err := landfreeze.Read(p)
			if err != nil || stored.Active || stored.ClearedSHA != r.ClearedSHA {
				t.Fatalf("clear must persist: %+v %v", stored, err)
			}
			if !strings.Contains(stderr.String(), "WARNING flake ledger not written:") {
				t.Fatalf("missing recovery warning: %s", &stderr)
			}
			wantCalls := 1
			if recovered {
				wantCalls = 2
			}
			if calls != wantCalls || len(verificationEvents(t, w.dir)) != 0 {
				t.Fatalf("recovery must retain e2e retry and flakes-only recording: calls=%d", calls)
			}
		})
	}
}

func TestLandedTrainFlakeLedgerWriteFailureWarns(t *testing.T) {
	for _, recovered := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "recovered"}[recovered], func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(`{"test":{"full":["full"],"e2e":["e2e"]}}`)
			w.forge("pushBeforeMerge", "other.txt")
			e := w.env(false)
			landedE2E := 0
			e.Shell = func(_ context.Context, dir, cmd string) (string, int) {
				if dir == w.dir && cmd == "e2e" {
					landedE2E++
					if landedE2E == 1 {
						if err := os.Remove(flakeledger.Path(w.dir)); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(flakeledger.Path(w.dir), 0o755); err != nil {
							t.Fatal(err)
						}
						if recovered {
							return "--- FAIL: TestE2E (0s)\nFAIL\n", 1
						}
					}
				}
				return "ok\n", 0
			}
			r, err := e.Train(bg, w.dir, TrainOpts{Targets: []string{"w1"}, Base: "main"})
			if err != nil || r.Verdict != landing.GateBaseMoved || !r.Changed || len(r.Landed) != 1 || !w.onOriginMain("work.txt") {
				t.Fatalf("ledger failure must preserve the landed-tree mismatch verdict: %+v %v", r, err)
			}
			if !strings.Contains(strings.Join(r.Notes, "\n"), "WARNING flake ledger not written:") {
				t.Fatalf("missing landed e2e warning: %+v", r.Notes)
			}
			wantCalls := 1
			if recovered {
				wantCalls = 2
			}
			if landedE2E != wantCalls {
				t.Fatalf("landed e2e calls=%d, want %d", landedE2E, wantCalls)
			}
			events := verificationEvents(t, w.dir)
			if len(events) != 4 {
				t.Fatalf("must retain full/e2e verification before and after landing: %+v", events)
			}
			last := events[len(events)-1]
			if timingDetail(t, last).Tier != tierE2E || last.PR != ghURL || last.Slot != "" || last.Issue != "" {
				t.Fatalf("landed e2e timing target lost: %+v", last)
			}
		})
	}
}
