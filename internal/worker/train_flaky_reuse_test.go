package worker

// These tests live apart from train_test.go: they share one subject (#4244, what a
// FLAKY train leaves in the cache) and that file is already large.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/testledger"
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

// A green rerun of a tier that carries an excuse or an earlier recovered flake
// is no clean pass: bisect must not cache it, or a retry would skip a tier the
// gate never saw pass cleanly (#4244). Only the clean case writes an entry.
func TestBisectGreenRerunCachesOnlyCleanPass(t *testing.T) {
	cases := []struct {
		name   string
		failed VerifyResult
		cached bool
	}{
		{"clean", VerifyResult{Verified: []string{"a"}, Failed: []string{"b"}}, true},
		{"excused", VerifyResult{Verified: []string{"a"}, Failed: []string{"b"}, Excluded: []testledger.Entry{{}}}, false},
		{"flaky", VerifyResult{Verified: []string{"a"}, Failed: []string{"b"}, Flaky: []flakeledger.Entry{{}}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := &verifyRun{
				res: &landing.TrainResult{}, cache: &trainCache{f: trainCacheFile{Verdicts: map[string]trainCacheVerdict{}}},
				baseSHA: "base", heads: []string{"h1"}, tips: []string{"t0", "t1"}, n: 1,
				phase: func(string, ...any) {},
				rerun: func(string, VerifyResult) (bool, error) { return true, nil },
			}
			if _, err := v.bisect("full", nil, tc.failed, "full"); err != nil {
				t.Fatal(err)
			}
			if _, ok := v.cache.get(trainKey("full", "base", []string{"h1"})); ok != tc.cached {
				t.Errorf("cached = %v, want %v", ok, tc.cached)
			}
		})
	}
}
