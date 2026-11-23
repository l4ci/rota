package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/landing"
)

// flakyOnceCmd fails with b2 merged until the counter file holds two bytes (the
// verify and its in-tree retry), then passes: the re-run is the green one.
// Every run appends to runs, so a test can tell a reused tier from a re-run.
func flakyOnceCmd(counter string) string {
	return fmt.Sprintf("printf r >> %s.runs; test ! -f b2.txt || [ $(cat %s 2>/dev/null | wc -c) -ge 2 ] || { printf x >> %s; %s; }",
		counter, counter, counter, goFail("TestRare", "ex.com/p"))
}

// The green re-run of a red tier is a pass of that tier on that base and those
// heads: a retry reuses it instead of running the full verify again (#4244).
func TestTrainFlakyRerunIsReusedByRetry(t *testing.T) {
	n := filepath.Join(t.TempDir(), "n")
	cmd := flakyOnceCmd(n)
	w := trainWorld(t, "", "b1", "b2")
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q],"vacuousCommand":%q}}`, cmd, ": {packages} {tests}; "+cmd))
	first, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || !first.Unreproduced || len(first.Landed) != 0 {
		t.Fatalf("AC-5: the first run is FLAKY and lands nothing: %+v %v", first, err)
	}
	before, _ := os.ReadFile(n + ".runs")
	second, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || second.Verdict != landing.GatePass || len(second.Landed) != 2 {
		t.Fatalf("the retry lands: %+v %v", second, err)
	}
	if len(second.CacheHits) == 0 {
		t.Errorf("AC-2: the retry reports the tier as reused: %+v", second)
	}
	if after, _ := os.ReadFile(n + ".runs"); string(after) != string(before) {
		t.Errorf("AC-2: the retry ran the verify again: %q -> %q", before, after)
	}
}

// A moved head misses the reused rerun: the retry verifies again (AC-3).
func TestTrainFlakyRerunNotReusedWhenHeadMoved(t *testing.T) {
	n := filepath.Join(t.TempDir(), "n")
	cmd := flakyOnceCmd(n)
	w := trainWorld(t, "", "b1", "b2")
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q],"vacuousCommand":%q}}`, cmd, ": {packages} {tests}; "+cmd))
	if first, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}}); err != nil || !first.Unreproduced {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := w.train(TrainOpts{Targets: []string{"b1"}}) // a different head set
	if err != nil || len(second.CacheHits) != 0 {
		t.Fatalf("AC-3: a different head set must not reuse the rerun: %+v %v", second, err)
	}
}

// A flake ledger that cannot be written is a warning on a FLAKY train, not an
// error that ends it (AC-4).
func TestTrainFlakyLedgerWriteFailureWarns(t *testing.T) {
	n := filepath.Join(t.TempDir(), "n")
	cmd := flakyOnceCmd(n)
	w := trainWorld(t, "", "b1", "b2")
	w.setConfig(fmt.Sprintf(`{"test":{"full":[%q],"vacuousCommand":%q}}`, cmd, ": {packages} {tests}; "+cmd))
	if err := os.MkdirAll(filepath.Join(w.dir, ".rota", "flakes.jsonl"), 0o755); err != nil { // a directory where the file belongs
		t.Fatal(err)
	}
	res, err := w.train(TrainOpts{Targets: []string{"b1", "b2"}})
	if err != nil || !res.Unreproduced || len(res.Landed) != 0 {
		t.Fatalf("AC-4/5: a ledger error leaves the FLAKY verdict: %+v %v", res, err)
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "flake ledger not written") {
		t.Errorf("AC-4: want a warning note, got %q", res.Notes)
	}
}
