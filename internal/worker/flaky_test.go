package worker

import (
	"context"
	"github.com/l4ci/rota/internal/gittest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/flakeledger"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/testledger"
)

func TestGateRetriesAssertionOnce(t *testing.T) {
	w := gateLedgerWorld(t, "check")
	e := w.env(false)
	calls := 0
	e.Shell = func(_ context.Context, dir, cmd string) (string, int) {
		calls++
		if dir == w.dir || cmd != "check" {
			t.Fatalf("check must run on the scratch merge: %s %s", dir, cmd)
		}
		if calls == 1 {
			return "--- FAIL: TestUnderLoad (0.01s)\nFAIL\n", 1
		}
		return "ok example/pkg\n", 0
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	t.Logf("checks=%d verdict=%s", calls, res.Verdict)
	if err != nil || res.Verdict != GatePass || !res.Changed || calls != 2 || !strings.Contains(strings.Join(res.Notes, "\n"), "FLAKY check") {
		t.Fatalf("one assertion failure followed by a pass must land green with a marker: %+v calls=%d err=%v", res, calls, err)
	}
}

func TestGateRetryEligibility(t *testing.T) {
	assertion := "--- FAIL: TestUnderLoad (0.01s)\nFAIL\n"
	for _, tc := range []struct {
		name, output string
		code, calls  int
	}{
		{"twice", assertion, 1, 2},
		{"unknown", "something broke\n", 1, 1},
		{"wrapper", "FAIL gotest (exit 1)\ngate: FAILED\n", 1, 1},
		{"build", "--- FAIL: TestA (0s)\nFAIL example/pkg [build failed]\n", 1, 1},
		{"setup", "--- FAIL: TestA (0s)\nFAIL example/pkg [setup failed]\n", 1, 1},
		{"panic", "panic: broken\n" + assertion, 1, 1},
		{"timeout", "panic: test timed out after 1s\n" + assertion, 1, 1},
		{"infra", "fork/exec compiler: resource temporarily unavailable\n" + assertion, 1, 1},
		{"killed", assertion, 137, 1},
		{"harness", assertion, 127, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := gateLedgerWorld(t, "check")
			e := w.env(false)
			calls := 0
			e.Shell = func(context.Context, string, string) (string, int) { calls++; return tc.output, tc.code }
			res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			t.Logf("checks=%d verdict=%s", calls, res.Verdict)
			if err != nil || res.Verdict != GateVerifyFailed || res.Changed || calls != tc.calls || strings.Contains(strings.Join(res.Notes, "\n"), "FLAKY") {
				t.Fatalf("%+v calls=%d err=%v", res, calls, err)
			}
			if _, err := os.Stat(flakeledger.Path(w.dir)); !os.IsNotExist(err) {
				t.Fatalf("a failure wrote a flake: %v", err)
			}
		})
	}
}

func TestGateRetryRequiresSameCleanTree(t *testing.T) {
	for _, mutateAt := range []int{1, 2} {
		w := gateLedgerWorld(t, "check")
		e := w.env(false)
		calls := 0
		e.Shell = func(_ context.Context, dir, cmd string) (string, int) {
			calls++
			if calls == mutateAt {
				if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("changed"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if calls == 1 {
				return "--- FAIL: TestUnderLoad (0s)\nFAIL\n", 1
			}
			return "ok\n", 0
		}
		res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
		if err != nil || res.Verdict != GateVerifyFailed || res.Changed || calls != mutateAt {
			t.Fatalf("mutation on run %d: %+v calls=%d err=%v", mutateAt, res, calls, err)
		}
	}
}

func TestGateFlakeLedgerRecordsBothRuns(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setPRBody("Closes #7")
	w.setConfig(`{"test":{"full":["check"]}}`)
	e := w.env(false)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	calls := 0
	var tree string
	first := "--- FAIL: TestUnderLoad (0s)\nFAIL\n"
	e.Shell = func(_ context.Context, dir, cmd string) (string, int) {
		calls++
		tree = gittest.Run(t, dir, "rev-parse", "HEAD^{tree}")
		if calls == 1 {
			return first, 1
		}
		return "ok example/pkg\n", 0
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if err != nil || res.Verdict != GatePass {
		t.Fatalf("%+v %v", res, err)
	}
	rows, err := flakeledger.Load(w.dir)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	f := rows[0]
	if f.Check != "check" || f.Tree != tree || len(f.PRs) != 1 || f.PRs[0] != ghURL || !f.TS.Equal(now) || f.First.Output != first || f.First.ExitCode != 1 || f.Retry.Output != "ok example/pkg\n" || f.Retry.ExitCode != 0 || strings.Join(f.Tests, ",") != "TestUnderLoad" {
		t.Fatalf("incomplete recovery: %+v", f)
	}
	es, err := ledger.Load(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range es {
		if entry.Kind == ledger.KindBounce {
			t.Fatal("a recovered assertion cost a bounce")
		}
	}
}

func TestTrainRetriesAssertionOnce(t *testing.T) {
	w := trainWorld(t, "check", "b1", "b2")
	e := w.env(false)
	calls := 0
	e.Shell = func(context.Context, string, string) (string, int) {
		calls++
		if calls == 1 {
			return "--- FAIL: TestUnderLoad (0s)\nFAIL\n", 1
		}
		return "ok\n", 0
	}
	res, err := e.Train(bg, w.dir, TrainOpts{Targets: []string{"b1", "b2"}, Base: "main"})
	if err != nil || res.Verdict != GatePass || len(res.Landed) != 2 || calls != 2 || !strings.Contains(strings.Join(res.Notes, "\n"), "FLAKY check") {
		t.Fatalf("%+v calls=%d err=%v", res, calls, err)
	}
}

func TestFlakyResultsDoNotEnterTrainCache(t *testing.T) {
	c := loadTrainCache(t.TempDir())
	c.put("tree", VerifyResult{Verified: []string{"check"}, Flaky: []flakeledger.Entry{{Check: "check"}}})
	if _, ok := c.get("tree"); ok {
		t.Fatal("a cached recovery would replay a flaky success without a fresh observation")
	}
}

func TestRetryFailureKeepsBothRunLogs(t *testing.T) {
	w := gateLedgerWorld(t, "check")
	e := w.env(false)
	calls := 0
	e.Shell = func(context.Context, string, string) (string, int) {
		calls++
		return "--- FAIL: TestUnderLoad (0s)\nFAIL\n", 1
	}
	vr, err := e.runGateVerify(bg, []string{"check"}, w.dir, testledger.Ledger{})
	if vr.LogPath != "" {
		defer os.Remove(vr.LogPath)
	}
	if err != nil || vr.OK() || calls != 2 || strings.Count(vr.Log, "--- FAIL: TestUnderLoad") != 2 || !strings.Contains(vr.Log, "== RETRY check") {
		t.Fatalf("both attempts must remain visible: %+v calls=%d err=%v", vr, calls, err)
	}
	b, err := os.ReadFile(vr.LogPath)
	if err != nil || string(b) != vr.Log {
		t.Fatalf("disk log differs: %s %v", b, err)
	}
}

func TestGateAndTrainRetryE2E(t *testing.T) {
	for _, train := range []bool{false, true} {
		w := gateLedgerWorld(t, "full")
		if train {
			w = trainWorld(t, "full", "b1")
		}
		w.setConfig(`{"test":{"full":["full"],"e2e":["e2e"]}}`)
		e := w.env(false)
		calls := 0
		e.Shell = func(_ context.Context, dir, cmd string) (string, int) {
			if cmd == "full" {
				return "ok\n", 0
			}
			calls++
			if calls == 1 {
				return "--- FAIL: TestE2E (0s)\nFAIL\n", 1
			}
			return "ok\n", 0
		}
		var verdict string
		if train {
			r, err := e.Train(bg, w.dir, TrainOpts{Targets: []string{"b1"}, Base: "main"})
			if err != nil {
				t.Fatal(err)
			}
			verdict = r.Verdict
		} else {
			r, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if err != nil {
				t.Fatal(err)
			}
			verdict = r.Verdict
		}
		rows, err := flakeledger.Load(w.dir)
		if verdict != GatePass || calls != 2 || err != nil || len(rows) != 1 || rows[0].Check != "e2e" {
			t.Fatalf("train=%v verdict=%s calls=%d rows=%+v err=%v", train, verdict, calls, rows, err)
		}
	}
}

func TestGateDoesNotLandWhenFlakeLedgerCannotBeWritten(t *testing.T) {
	w := gateLedgerWorld(t, "check")
	if err := os.Mkdir(flakeledger.Path(w.dir), 0o755); err != nil {
		t.Fatal(err)
	}
	e := w.env(false)
	calls := 0
	e.Shell = func(context.Context, string, string) (string, int) {
		calls++
		if calls == 1 {
			return "--- FAIL: TestLoad (0s)\nFAIL\n", 1
		}
		return "ok\n", 0
	}
	r, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	if (err == nil && r.Verdict == GatePass) || r.Changed || w.onOriginMain("work.txt") {
		t.Fatalf("recovery without a durable record must not land: %+v %v", r, err)
	}
}

func TestCancelledAssertionDoesNotRetry(t *testing.T) {
	w := gateLedgerWorld(t, "check")
	e := w.env(false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	e.Shell = func(context.Context, string, string) (string, int) {
		calls++
		cancel()
		return "--- FAIL: TestLoad (0s)\nFAIL\n", 1
	}
	r, err := e.runGateVerify(ctx, []string{"check"}, w.dir, testledger.Ledger{})
	if r.LogPath != "" {
		defer os.Remove(r.LogPath)
	}
	if err != nil || r.OK() || calls != 1 || len(r.Flaky) != 0 {
		t.Fatalf("%+v calls=%d err=%v", r, calls, err)
	}
}

func TestGateRetryReportsDurationAndTests(t *testing.T) {
	w := gateLedgerWorld(t, "check")
	e := w.env(false)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	calls := 0
	e.Shell = func(context.Context, string, string) (string, int) {
		calls++
		if calls == 1 {
			return "--- FAIL: TestUnderLoad (0s)\n--- FAIL: TestOther (0s)\nFAIL\n", 1
		}
		now = now.Add(1250 * time.Millisecond)
		return "ok\n", 0
	}
	res, err := e.Gate(bg, w.dir, GateOpts{Slot: "w1", Base: "main"})
	want := "FLAKY check — tests: TestUnderLoad, TestOther; rerun: 1.250s"
	if err != nil || res.Verdict != GatePass || !strings.Contains(strings.Join(res.Notes, "\n"), want) {
		t.Fatalf("missing rerun diagnostics: %+v %v", res, err)
	}
}

func TestTrainRetryReportsDurationAndTests(t *testing.T) {
	w := trainWorld(t, "check", "b1")
	e := w.env(false)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	calls := 0
	e.Shell = func(context.Context, string, string) (string, int) {
		calls++
		if calls == 1 {
			return "--- FAIL: TestLoad (0s)\nFAIL\n", 1
		}
		now = now.Add(2500 * time.Millisecond)
		return "ok\n", 0
	}
	r, err := e.Train(bg, w.dir, TrainOpts{Targets: []string{"b1"}, Base: "main"})
	if err != nil || r.Verdict != GatePass || len(r.Flaky) != 1 || r.Flaky[0].Retry.WallSeconds != 2.5 || !strings.Contains(strings.Join(r.Notes, "\n"), "tests: TestLoad; rerun: 2.500s") {
		t.Fatalf("%+v %v", r, err)
	}
	rows, err := flakeledger.Load(w.dir)
	if err != nil || len(rows) != 1 || rows[0].Retry.WallSeconds != 2.5 {
		t.Fatalf("duration not persisted: %+v %v", rows, err)
	}
}
