package worker

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/testledger"
)

func subcheckMarker(t *testing.T, check, first, retry string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"check": check, "firstExit": 1, "retryExit": 0, "firstOutput": first, "retryOutput": retry, "retrySeconds": 0.125})
	if err != nil {
		t.Fatal(err)
	}
	return "ROTA-FLAKY " + string(b) + "\n"
}

func TestGateRecordsSubcheckRecovery(t *testing.T) {
	for _, tc := range []struct{ check, first, test string }{
		{"smoke-shard2", "  FAIL controlled smoke case\n", "smoke-shard2"},
		{"go:internal/harness", "--- FAIL: TestUnderLoad (0s)\nFAIL\n", "TestUnderLoad"},
	} {
		t.Run(tc.check, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setPRBody("Closes #7")
			w.setConfig(`{"test":{"full":["bash test/gate.sh"]}}`)
			e := w.env(false)
			now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
			e.Now = func() time.Time { return now }
			calls, tree := 0, ""
			e.Shell = func(_ context.Context, dir, command string) (string, int) {
				calls++
				tree = gitq(t, dir, "rev-parse", "HEAD^{tree}")
				return subcheckMarker(t, tc.check, tc.first, "ok\n") + "All smoke tests passed.\n", 0
			}
			r, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if err != nil || r.Verdict != GatePass || calls != 1 {
				t.Fatalf("recovered wrapper must pass without rerunning: %+v calls=%d err=%v", r, calls, err)
			}
			rows, err := flakeledger.Load(w.dir)
			if err != nil || len(rows) != 1 {
				t.Fatalf("sub-check recovery must reach the shared flake ledger: rows=%+v err=%v", rows, err)
			}
			f := rows[0]
			if f.Check != tc.check || strings.Join(f.Tests, ",") != tc.test || f.Tree != tree || !f.TS.Equal(now) || len(f.PRs) != 1 || f.PRs[0] != ghURL || f.First.ExitCode != 1 || f.First.Output != tc.first || f.Retry.ExitCode != 0 || f.Retry.Output != "ok\n" || f.Retry.WallSeconds != 0.125 {
				t.Fatalf("incomplete sub-check record: %+v", f)
			}
			if len(r.Flaky) != 1 || r.Flaky[0].Check != tc.check || !strings.Contains(strings.Join(r.Notes, "\n"), "FLAKY "+tc.check) || !strings.Contains(strings.Join(r.Notes, "\n"), "rerun: 0.125s") {
				t.Fatalf("sub-check must be named in the verdict: %+v", r)
			}
		})
	}
}

func TestTrainRecordsSubcheckRecovery(t *testing.T) {
	w := trainWorld(t, "wrapper", "b1", "b2")
	e := w.env(false)
	calls := 0
	e.Shell = func(context.Context, string, string) (string, int) {
		calls++
		return subcheckMarker(t, "smoke-shard1", "FAIL case\n", "ok\n"), 0
	}
	r, err := e.Train(bg, w.dir, TrainOpts{Targets: []string{"b1", "b2"}, Base: "main"})
	rows, loadErr := flakeledger.Load(w.dir)
	if err != nil || r.Verdict != GatePass || len(r.Landed) != 2 || calls != 1 || loadErr != nil || len(rows) != 1 || rows[0].Check != "smoke-shard1" || (len(r.Flaky) != 1 || r.Flaky[0].Check != "smoke-shard1") {
		t.Fatalf("train must reuse the same sub-check ledger path: %+v calls=%d rows=%+v err=%v load=%v", r, calls, rows, err, loadErr)
	}
}

func TestGateKeepsRealFailureAlongsideSubcheckRecovery(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	w := gateLedgerWorld(t, "wrapper")
	e := w.env(false)
	calls := 0
	e.Shell = func(context.Context, string, string) (string, int) {
		calls++
		return subcheckMarker(t, "smoke-shard1", "FAIL case\n", "ok\n") + "FAIL gofmt (exit 1)\ngate: FAILED\n", 1
	}
	r, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	rows, loadErr := flakeledger.Load(w.dir)
	if err != nil || r.Verdict != GateVerifyFailed || r.Changed || calls != 1 || loadErr != nil || len(rows) != 1 || rows[0].Check != "smoke-shard1" {
		t.Fatalf("a recovered shard must not excuse another failed check or retry the wrapper: %+v calls=%d rows=%+v err=%v load=%v", r, calls, rows, err, loadErr)
	}
}

func TestSubcheckMalformedAndEmbeddedRecords(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	for _, tc := range []struct {
		name, output string
		ok           bool
	}{
		{"bad json", "ROTA-FLAKY {broken}\n", false},
		{"missing outcomes", `ROTA-FLAKY {"check":"smoke-shard1"}`, false},
		{"missing retry code", `ROTA-FLAKY {"check":"smoke-shard1","firstExit":1,"firstOutput":"fail","retryOutput":"ok"}`, false},
		{"failed retry", `ROTA-FLAKY {"check":"smoke-shard1","firstExit":1,"retryExit":1,"firstOutput":"fail","retryOutput":"fail"}`, false},
		{"deterministic check", subcheckMarker(t, "gofmt", "bad", "good"), false},
		{"embedded log", "     " + subcheckMarker(t, "smoke-shard1", "fail", "ok"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := gateLedgerWorld(t, "wrapper")
			e := w.env(false)
			calls := 0
			e.Shell = func(context.Context, string, string) (string, int) { calls++; return tc.output, 0 }
			vr, err := e.runGateVerify(bg, []string{"wrapper"}, w.dir, testledger.Ledger{})
			if vr.LogPath != "" {
				defer os.Remove(vr.LogPath)
			}
			if err != nil || vr.OK() != tc.ok || len(vr.Flaky) != 0 || calls != 1 {
				t.Fatalf("invalid/embedded markers must not produce observations: %+v calls=%d err=%v", vr, calls, err)
			}
		})
	}
}
