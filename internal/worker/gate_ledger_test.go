package worker

import (
	"testing"

	"github.com/l4ci/rota/internal/ledger"
)

func gateKinds(t *testing.T, w *world) (verdicts []string, merges int) {
	t.Helper()
	es, err := ledger.Load(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		switch e.Kind {
		case ledger.KindGate:
			verdicts = append(verdicts, e.DetailStr("verdict"))
		case ledger.KindMerge:
			merges++
		}
	}
	return verdicts, merges
}

func TestGateAppendsVerdict(t *testing.T) {
	w := newWorld(t, ghURL)
	if res, err := w.gate(false, GateOpts{}); err != nil || res.Verdict != GatePass {
		t.Fatalf("gate = %+v, %v", res, err)
	}
	verdicts, merges := gateKinds(t, w)
	if len(verdicts) != 1 || verdicts[0] != GatePass || merges != 1 {
		t.Fatalf("verdicts %v merges %d", verdicts, merges)
	}
	es, _ := ledger.Load(w.dir)
	if e := es[0]; e.Slot != "w1" || e.PR != ghURL {
		t.Errorf("gate entry = %+v", e)
	}
}

func TestGateLedgerBlockedMergesNothing(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(`{"test":{"full":["true","false"]}}`)
	if res, err := w.gate(false, GateOpts{}); err != nil || res.Verdict != GateVerifyFailed {
		t.Fatalf("gate = %+v, %v", res, err)
	}
	verdicts, merges := gateKinds(t, w)
	if len(verdicts) != 1 || verdicts[0] != GateVerifyFailed || merges != 0 {
		t.Fatalf("verdicts %v merges %d", verdicts, merges)
	}
}

func TestGateLedgerSkipsCheckOnly(t *testing.T) {
	w := newWorld(t, ghURL)
	if _, err := w.gate(false, GateOpts{CheckOnly: true}); err != nil {
		t.Fatal(err)
	}
	if ledger.Exists(w.dir) {
		t.Error("a read-only gate check wrote the ledger")
	}
}

// A train that fails records its verdict for every member it gated, not only
// for the ones that landed: here none did.
func TestTrainLedgerRecordsCulpritAndNonLandingMembers(t *testing.T) {
	w, res := ciTrain(t, "", "b3.txt", "b1", "b2", "b3", "b4")
	if res.Verdict != GateVerifyFailed || res.Culprit != "b3" {
		t.Fatalf("%+v", res)
	}
	es, err := ledger.Load(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range es {
		if e.Kind == ledger.KindGate {
			got[e.Slot] = e.DetailStr("verdict")
		}
		if e.Kind == ledger.KindMerge {
			t.Errorf("a train that landed nothing wrote a merge: %+v", e)
		}
	}
	for _, s := range []string{"b1", "b2", "b3", "b4"} {
		if got[s] != GateVerifyFailed {
			t.Errorf("member %s verdict %q, want %s (all: %v)", s, got[s], GateVerifyFailed, got)
		}
	}
}
