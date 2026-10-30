package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
)

func TestTrainSummaryCountsInvocations(t *testing.T) {
	w := trainWorld(t, "test ! -f b2.txt", "b1", "b2", "b3")
	targets := []string{"b1", "b2", "b3"}
	if _, err := w.train(TrainOpts{Targets: targets, Plan: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.train(TrainOpts{}); err == nil {
		t.Fatal("empty train should be a usage error")
	}
	if res, err := w.train(TrainOpts{Targets: targets}); err != nil || res.Verdict != GateVerifyFailed {
		t.Fatalf("red train: %+v, %v", res, err)
	}
	w.setConfig(`{"test":{"full":["true"]}}`)
	if res, err := w.train(TrainOpts{Targets: targets}); err != nil || res.Verdict != GatePass {
		t.Fatalf("green train: %+v, %v", res, err)
	}
	es, err := ledger.Load(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	s := ledger.Fold(es, nil, 0)
	v, _ := s.Object().Get("metrics")
	metrics := v.(*jsonx.Object)
	v, _ = metrics.Get("redTrainRate")
	rate, ok := v.(*jsonx.Object)
	if !ok {
		t.Fatalf("red-train metric missing: %v", v)
	}
	for key, want := range map[string]any{"numerator": 1, "denominator": 2, "value": 0.5, "unavailable": nil} {
		if got, _ := rate.Get(key); got != want {
			t.Errorf("%s = %v, want %v; count train invocations, not members or previews", key, got, want)
		}
	}
	if len(s.Issues) != 3 {
		t.Errorf("train outcomes created phantom issue rows: %+v", s.Issues)
	}
}

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

// A train that fails records an entry for every member it gated, not only for
// the ones that landed: here none did. Only the culprit carries the verdict;
// the others name it.
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
			if e.DetailStr("culprit") != "b3" {
				t.Errorf("member %s entry names culprit %q, want b3", e.Slot, e.DetailStr("culprit"))
			}
		}
		if e.Kind == ledger.KindMerge {
			t.Errorf("a train that landed nothing wrote a merge: %+v", e)
		}
	}
	for _, s := range []string{"b1", "b2", "b3", "b4"} {
		want := ""
		if s == "b3" {
			want = GateVerifyFailed
		}
		if v, ok := got[s]; !ok || v != want {
			t.Errorf("member %s verdict %q (recorded %v), want %q (all: %v)", s, v, ok, want, got)
		}
	}
}

// A PR no slot owns has no issue of its own: the ledger records the target as
// resolved, not the issue its forge head branch names, which only the closes
// check reads.
func TestGateLedgerExternalPRKeepsResolvedTarget(t *testing.T) {
	w := newWorld(t, "")
	if err := os.WriteFile(filepath.Join(w.dir, ".rota", "workers.json"), []byte(`{"slots":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, w.worker, "push", "-q", "origin", "w1:dana/7-x")
	w.forge("head", "dana/7-x")
	w.setPRBody("Closes #7")
	res, err := w.env(false).Gate(bg, w.dir, GateOpts{Slot: ghURL, Base: "main", NoVerify: true})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("gate = %+v, %v", res, err)
	}
	es, err := ledger.Load(w.dir)
	if err != nil || len(es) == 0 {
		t.Fatalf("ledger %v, %v", es, err)
	}
	for _, e := range es {
		if e.Issue != "" {
			t.Errorf("%s entry records issue %q for an unowned external PR, want none", e.Kind, e.Issue)
		}
	}
}
