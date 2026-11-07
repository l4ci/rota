package worker

import (
	"context"
	"encoding/json"
	"github.com/l4ci/rota/internal/jsonx"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/testledger"
)

func TestGateVerificationMeasuresRetryAndFailure(t *testing.T) {
	w := gateLedgerWorld(t, "check")
	e := w.env(false)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	calls := 0
	e.Shell = func(context.Context, string, string) (string, int) {
		calls++
		now = now.Add(2 * time.Second)
		if calls == 1 {
			return "--- FAIL: TestLoad (0s)\nFAIL\n", 1
		}
		if calls == 2 {
			return "ok\n", 0
		}
		return "unknown failure\n", 9
	}
	r, err := e.runGateVerify(bg, []string{"check", "broken"}, w.dir, testledger.Ledger{})
	if r.LogPath != "" {
		defer os.Remove(r.LogPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		WallSeconds *float64
		Suites      []struct {
			Command     string
			WallSeconds float64
			ExitCode    int
		}
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.WallSeconds == nil || *got.WallSeconds != 6 || len(got.Suites) != 2 {
		t.Fatalf("missing measured gate timing: %s; want total 6 seconds and two suites", b)
	}
	if got.Suites[0].Command != "check" || got.Suites[0].WallSeconds != 4 || got.Suites[0].ExitCode != 0 || got.Suites[1].Command != "broken" || got.Suites[1].WallSeconds != 2 || got.Suites[1].ExitCode != 9 {
		t.Fatalf("retry and failed command timing lost: %s", b)
	}
}

func verificationEvents(t *testing.T, root string) []ledger.Entry {
	t.Helper()
	entries, err := ledger.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var events []ledger.Entry
	for _, e := range entries {
		if e.Kind == "verification" {
			events = append(events, e)
		}
	}
	return events
}

func TestVerificationEventsMatchLocalTierRuns(t *testing.T) {
	for _, train := range []bool{false, true} {
		t.Run(map[bool]string{false: "gate", true: "train"}[train], func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setPRBody("Closes #7")
			if train {
				w = trainWorld(t, "full", "b1", "b2")
			}
			w.setConfig(`{"test":{"full":["full"],"e2e":["e2e"]}}`)
			e := w.env(false)
			now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			e.Now = func() time.Time { return now }
			calls := 0
			e.Shell = func(context.Context, string, string) (string, int) {
				calls++
				now = now.Add(1250 * time.Millisecond)
				return "ok\n", 0
			}
			var verdict string
			if train {
				r, err := e.Train(bg, w.dir, TrainOpts{Targets: []string{"b1", "b2"}, Base: "main"})
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
			events := verificationEvents(t, w.dir)
			if verdict != GatePass || calls < 2 || len(events) != calls {
				t.Fatalf("one observation per executed tier: verdict=%s calls=%d events=%+v", verdict, calls, events)
			}
			for i, event := range events {
				d := timingDetail(t, event)
				if d.WallSeconds != 1.25 || len(d.Suites) != 1 || d.Suites[0].WallSeconds != 1.25 || d.Suites[0].ExitCode != 0 || (d.Tier != "test.full" && d.Tier != "test.e2e") || d.Suites[0].Command != map[string]string{"test.full": "full", "test.e2e": "e2e"}[d.Tier] {
					t.Fatalf("event %d: %+v", i, d)
				}
				if !train && (event.PR != ghURL || event.Slot != "w1" || event.Issue != "") {
					t.Fatalf("gate identity lost: %+v", event)
				}
				if event.TS.IsZero() || event.TS.After(now) {
					t.Fatalf("timestamp must use injected clock: %s", event.TS)
				}
			}
			if !train && !events[0].TS.Equal(time.Date(2026, 11, 6, 12, 0, 1, 0, time.UTC)) {
				t.Fatalf("timestamp not from injected clock: %s", events[0].TS)
			}
		})
	}
}

type measuredDetail struct {
	Tier        string  `json:"tier"`
	WallSeconds float64 `json:"wallSeconds"`
	Suites      []struct {
		Command     string  `json:"command"`
		WallSeconds float64 `json:"wallSeconds"`
		ExitCode    int     `json:"exitCode"`
	} `json:"suites"`
}

func timingDetail(t *testing.T, event ledger.Entry) measuredDetail {
	t.Helper()
	b, err := jsonx.MarshalCompact(event.Detail)
	if err != nil {
		t.Fatal(err)
	}
	var d measuredDetail
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestTrainVerificationDoesNotReplayCachedTiming(t *testing.T) {
	w := trainWorld(t, "check", "b1", "b2")
	e := w.env(false)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	e.Now = func() time.Time { return now }
	e.Shell = func(_ context.Context, dir, _ string) (string, int) {
		now = now.Add(2 * time.Second)
		if _, err := os.Stat(filepath.Join(dir, "b2.txt")); err == nil {
			return "red\n", 3
		}
		return "ok\n", 0
	}
	for i := 0; i < 2; i++ {
		r, err := e.Train(bg, w.dir, TrainOpts{Targets: []string{"b1", "b2"}, Base: "main"})
		if err != nil || r.Verdict != GateVerifyFailed || r.Culprit != "b2" {
			t.Fatalf("%+v %v", r, err)
		}
		if i == 1 && len(r.CacheHits) != 1 {
			t.Fatalf("expected green prefix cache hit: %+v", r)
		}
	}
	events := verificationEvents(t, w.dir)
	// First run observes the red train and green prefix; second observes only red.
	if len(events) != 3 {
		t.Fatalf("want three real runs across bisection/cache, got %+v", events)
	}
	for i, wantCode := range []int{3, 0, 3} {
		d := timingDetail(t, events[i])
		if d.Tier != "test.full" || d.WallSeconds != 2 || len(d.Suites) != 1 || d.Suites[0].ExitCode != wantCode {
			t.Fatalf("event %d: %+v", i, d)
		}
	}
	if got := len(ledger.Fold(events, nil, 0).Issues); got != 0 {
		t.Fatalf("timings created %d phantom issue rows", got)
	}
}

func TestVerificationUnavailableForCIAndEmptyTiers(t *testing.T) {
	w := newWorld(t, ghURL)
	w.setConfig(ciCfg)
	r, err := w.ciGate(GateOpts{})
	if err != nil || r.Verdict != GatePass {
		t.Fatalf("%+v %v", r, err)
	}
	if events := verificationEvents(t, w.dir); len(events) != 0 {
		t.Fatalf("CI invented measurements: %+v", events)
	}
	e := w.env(false)
	empty, err := e.runGateVerify(bg, nil, w.dir, testledger.Ledger{})
	if err != nil || empty.WallSeconds != nil || len(empty.Suites) != 0 {
		t.Fatalf("empty tier must remain unmeasured: %+v %v", empty, err)
	}
	e.recordVerification(w.dir, nil, tierFull, GateTarget{}, nil, empty)
	// Defensive: reused/CI answers must never write even if they carry stale timing.
	elapsed := 4.0
	for _, vr := range []VerifyResult{{Cached: true, WallSeconds: &elapsed, Suites: []VerificationSuite{{Command: "old"}}}, {CI: true, WallSeconds: &elapsed, Suites: []VerificationSuite{{Command: "old"}}}} {
		e.recordVerification(w.dir, nil, tierFull, GateTarget{}, nil, vr)
	}
	if events := verificationEvents(t, w.dir); len(events) != 0 {
		t.Fatalf("unavailable tiers wrote measurements: %+v", events)
	}
}
