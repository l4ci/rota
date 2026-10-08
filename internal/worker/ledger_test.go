package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/ledger"
)

func (w *world) setLedger(body string) {
	os.WriteFile(filepath.Join(w.dir, ".rota", "test-ledger.json"), []byte(body), 0o644)
}

const (
	failFlaky = `echo '--- FAIL: TestFlaky (0.00s)'; echo 'FAIL'; false`
	futureDay = "2999-01-01"
	pastDay   = "2000-01-01"
)

func ledgerEntry(test, expires string) string {
	return `[{"test":"` + test + `","owner":"dana","receipt":"#378","expires":"` + expires + `"}]`
}

func gateLedgerWorld(t *testing.T, full string) *world {
	t.Helper()
	w := newWorld(t, "")
	gitq(t, w.dir, "fetch", "-q", "origin", "w1:w1")
	w.setConfig(`{"test":{"full":[` + jsonString(full) + `]}}`)
	return w
}

func jsonString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func TestGateLedgerUnexpiredEntryExcludesAndIsListed(t *testing.T) {
	w := gateLedgerWorld(t, failFlaky)
	w.setLedger(ledgerEntry("TestFlaky", futureDay))
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GatePass || len(res.Excluded) != 1 || res.Excluded[0].Test != "TestFlaky" || res.Excluded[0].Receipt != "#378" {
		t.Fatalf("an unexpired entry should excuse the failing test and list it: %+v %v", res, err)
	}
}

func TestGateLedgerNoEntryStillFails(t *testing.T) {
	w := gateLedgerWorld(t, failFlaky)
	w.setLedger(ledgerEntry("TestOther", futureDay))
	res, _ := w.gate(false, GateOpts{})
	if res.Verdict != GateVerifyFailed || len(res.Excluded) != 0 {
		t.Fatalf("a failing test with no entry must fail: %+v", res)
	}
}

func TestGateLedgerExpiredEntryFailsAndIsNamed(t *testing.T) {
	w := gateLedgerWorld(t, "true")
	w.setLedger(ledgerEntry("TestFlaky", pastDay))
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GateVerifyFailed || res.Changed {
		t.Fatalf("an expired entry fails the gate before anything merges: %+v %v", res, err)
	}
	for _, want := range []string{"TestFlaky", "dana", "#378"} {
		if !strings.Contains(res.Err, want) {
			t.Errorf("failure should name %q: %s", want, res.Err)
		}
	}
	if len(res.Expired) != 1 {
		t.Errorf("expired entries are in the result: %+v", res.Expired)
	}
}

func TestGateLedgerEmptyOrMissingIsNoOp(t *testing.T) {
	w := gateLedgerWorld(t, "true")
	if res, err := w.gate(false, GateOpts{}); err != nil || res.Verdict != GatePass || len(res.Excluded)+len(res.Expired) != 0 {
		t.Fatalf("missing ledger: %+v %v", res, err)
	}
	w = gateLedgerWorld(t, failFlaky)
	w.setLedger("[]")
	if res, _ := w.gate(false, GateOpts{}); res.Verdict != GateVerifyFailed {
		t.Fatalf("an empty ledger excludes nothing: %+v", res)
	}
}

func TestGateLedgerMalformedIsExit2(t *testing.T) {
	w := gateLedgerWorld(t, "true")
	w.setLedger(`[{"test":"TestFlaky","owner":"dana"}]`)
	_, err := w.gate(false, GateOpts{})
	if err == nil || !strings.Contains(err.Error(), "entry 1 (TestFlaky)") {
		t.Fatalf("a malformed entry should be named: %v", err)
	}
}

func TestTrainLedgerExcludesExpiredFailsAndNoEntryFails(t *testing.T) {
	w := trainWorld(t, failFlaky, "b1", "b2")
	w.setLedger(ledgerEntry("TestFlaky", futureDay))
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || res.Verdict != GatePass || len(res.Excluded) != 1 || res.Excluded[0].Test != "TestFlaky" {
		t.Fatalf("unexpired entry should excuse the train: %+v %v", res, err)
	}

	w = trainWorld(t, failFlaky, "b1")
	w.setLedger(ledgerEntry("TestFlaky", pastDay))
	res, err = w.train(TrainOpts{Targets: []string{"b1"}})
	if err != nil || res.Verdict != GateVerifyFailed || len(res.Expired) != 1 || len(res.Landed) != 0 ||
		!strings.Contains(res.Err, "TestFlaky") || !strings.Contains(res.Err, "dana") || !strings.Contains(res.Err, "#378") {
		t.Fatalf("an expired entry fails the train and is named: %+v %v", res, err)
	}

	w = trainWorld(t, failFlaky, "b1")
	res, _ = w.train(TrainOpts{Targets: []string{"b1"}})
	if res.Verdict != GateVerifyFailed || w.onMain("b1.txt") {
		t.Fatalf("no entry: the train must fail and land nothing: %+v", res)
	}
}

// A PR merged on the forge before the gate ran gets a merge entry of its own:
// the gate did not land it, so the entry says by: remote.
func TestGateLedgerRecordsARemoteMerge(t *testing.T) {
	w := newWorld(t, ghURL)
	w.forge("state", "MERGED")
	res, err := w.gate(false, GateOpts{})
	if err != nil || res.Verdict != GatePRMismatch {
		t.Fatalf("gate: %+v %v", res, err)
	}
	var merges []ledger.Entry
	es, _ := ledger.Load(w.dir)
	for _, e := range es {
		if e.Kind == ledger.KindMerge {
			merges = append(merges, e)
		}
	}
	if len(merges) != 1 || merges[0].DetailStr("by") != "remote" {
		t.Fatalf("merge entries %+v, want one with by: remote", merges)
	}
}
