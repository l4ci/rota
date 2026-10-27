package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/ledger"
)

func TestRoundSummaryUnavailableSources(t *testing.T) {
	for _, kind := range []string{"missing", "unreadable", "invalid timestamp"} {
		t.Run(kind, func(t *testing.T) {
			deps := testDeps()
			dir := workerProject(t, `{}`)
			switch kind {
			case "unreadable":
				if err := os.Mkdir(ledger.Path(dir), 0o755); err != nil {
					t.Fatal(err)
				}
			case "malformed", "invalid timestamp":
				seedLedger(t, dir)
				bad := "{torn\n"
				if kind == "invalid timestamp" {
					bad = `{"ts":"wrong","round":2,"kind":"bounce"}` + "\n"
				}
				f, err := os.OpenFile(ledger.Path(dir), os.O_APPEND|os.O_WRONLY, 0o644)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString(bad)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			code, out, stderr := rotaInWith(t, deps, dir, "round", "summary", "--json")
			if code != 0 {
				t.Fatalf("summary must report unavailable sources: exit %d, %s %s", code, out, stderr)
			}
			metrics := data(t, out)["metrics"].(map[string]any)
			for _, name := range []string{"assignToMerged", "doneToMerged", "redTrainRate", "bouncesPerMergedPR"} {
				m := metrics[name].(map[string]any)
				reason, _ := m["unavailable"].(string)
				if !strings.Contains(reason, "ledger") {
					t.Errorf("%s needs its source-error reason, got %v", name, m)
				}
				for _, key := range []string{"value", "numerator", "denominator", "medianSeconds", "p90Seconds", "samples"} {
					if m[key] != nil {
						t.Errorf("%s.%s = %v, want null for unavailable source", name, key, m[key])
					}
				}
			}
			code, out, _ = rotaInWith(t, deps, dir, "round", "summary")
			if code != 0 || !strings.Contains(out, "unavailable (") || !strings.Contains(out, "rule:") {
				t.Errorf("text must name unavailable source and counting rules: exit %d, %s", code, out)
			}
			if kind == "missing" && (!strings.Contains(out, "unavailable (no ledger)") || strings.Contains(out, dir)) {
				t.Errorf("missing source needs a stable reason without an absolute path: %s", out)
			}
		})
	}
}

func TestRoundSummaryCorruptionIsScoped(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		available  bool
	}{
		{"unidentifiable torn line", "{torn\n", true},
		{"other round invalid event", `{"round":1,"kind":"bounce","ts":"wrong"}` + "\n", true},
		{"own round invalid event", `{"round":2,"kind":"bounce","ts":"wrong"}` + "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := workerProject(t, `{}`)
			seedLedger(t, dir)
			b, err := os.ReadFile(ledger.Path(dir))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(ledger.Path(dir), append(b, tc.line...), 0o644); err != nil {
				t.Fatal(err)
			}
			deps := testDeps()
			code, out, _ := rotaInWith(t, deps, dir, "round", "summary", "--round", "2", "--json")
			if code != 0 {
				t.Fatalf("exit %d: %s", code, out)
			}
			d := data(t, out)
			if d["skippedLedgerLines"] != float64(1) {
				t.Errorf("skipped line count = %v, want 1", d["skippedLedgerLines"])
			}
			m := d["metrics"].(map[string]any)["assignToMerged"].(map[string]any)
			var want any
			if tc.available {
				want = float64(1260)
			}
			if m["medianSeconds"] != want {
				t.Errorf("median = %v, want %v; corruption must stay within its identified round", m, want)
			}
			if !tc.available && m["samples"] != nil {
				t.Errorf("unavailable samples = %v, want null", m["samples"])
			}
			if code, out, _ := rotaInWith(t, deps, dir, "round", "summary", "--round", "9"); code != 3 {
				t.Errorf("unknown round with corrupt source: exit %d, want 3: %s", code, out)
			}
			obj, _, ok := windDownSummary(dir, 2)
			if !ok {
				t.Fatal("wind-down lost the readable summary")
			}
			v, _ := obj.Get("metrics")
			v, _ = v.(*jsonx.Object).Get("assignToMerged")
			metric := v.(*jsonx.Object)
			v, _ = metric.Get("medianSeconds")
			if (v != nil) != tc.available {
				t.Errorf("wind-down availability differs from summary: %v", v)
			}
		})
	}
}

func TestRoundSummaryIdentifiesDamagedRound(t *testing.T) {
	dir := workerProject(t, `{}`)
	if err := os.WriteFile(ledger.Path(dir), []byte(`{"round":4,"kind":"merge","ts":"wrong"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := rotaInWith(t, testDeps(), dir, "round", "summary", "--json")
	if code != 0 || data(t, out)["round"] != float64(4) {
		t.Fatalf("default must select the highest identifiable round: exit %d: %s", code, out)
	}
	obj, text, ok := windDownSummary(dir, 4)
	if !ok || obj == nil || !strings.Contains(text, "unavailable (ledger has unreadable events for round 4)") {
		t.Errorf("wind-down must report the identified damaged round: %s", text)
	}
}

func seedLedger(t *testing.T, dir string) {
	t.Helper()
	t0 := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	for _, e := range []ledger.Entry{
		{TS: t0, Kind: ledger.KindAssign, Round: 2, Issue: "12", Slot: "ben", Account: "work", Harness: "claude", Detail: ledger.Detail("headroom", 80.0)},
		{TS: t0.Add(20 * time.Minute), Kind: ledger.KindDone, Round: 2, Issue: "12", Slot: "ben", PR: "#7", Detail: ledger.Detail("headroom", 55.0)},
		{TS: t0.Add(21 * time.Minute), Kind: ledger.KindGate, Round: 2, Issue: "12", Slot: "ben", PR: "#7", Detail: ledger.Detail("verdict", "pass")},
		{TS: t0.Add(21 * time.Minute), Kind: ledger.KindMerge, Round: 2, Issue: "12", Slot: "ben", PR: "#7"},
		{TS: t0, Kind: ledger.KindAssign, Round: 1, Issue: "3", Slot: "dana", Harness: "codex"},
	} {
		if err := ledger.Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRoundSummaryText(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	seedLedger(t, dir)
	code, out, _ := rotaInWith(t, deps, dir, "round", "summary")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"Round 2", "quota share", "QUOTA SHARE", "25%", "ben", "work", "Gate audit", "median 21m00s, p90 21m00s (1 PR)", "median 1m00s, p90 1m00s (1 PR)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "dana") {
		t.Errorf("round 1 must not show in round 2's summary:\n%s", out)
	}
	_, out, _ = rotaInWith(t, deps, dir, "round", "summary", "--round", "1")
	if !strings.Contains(out, "dana") || !strings.Contains(out, "n/a") {
		t.Errorf("--round 1:\n%s", out)
	}
}

func TestRoundSummaryJSON(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	seedLedger(t, dir)
	code, out, _ := rotaInWith(t, deps, dir, "round", "summary", "--json")
	d := data(t, out)
	issues, _ := d["issues"].([]any)
	if code != 0 || d["round"] != float64(2) || len(issues) != 1 || d["slots"] == nil || d["accounts"] == nil || d["audit"] == nil {
		t.Fatalf("%d %v", code, d)
	}
	row := issues[0].(map[string]any)
	if row["quotaShare"] != float64(25) || row["merged"] != true || row["gate"] != "pass" {
		t.Errorf("row = %v", row)
	}
	if _, exists := d["metrics"].(map[string]any)["flakeRate"]; exists {
		t.Error("flake metric belongs to #2171 and must be absent")
	}
}

func TestRoundSummaryNoLedgerExitsZero(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	code, out, _ := rotaInWith(t, deps, dir, "round", "summary")
	if code != 0 || !strings.Contains(out, "no ledger yet") {
		t.Errorf("%d %q", code, out)
	}
}

func TestRoundSummaryUnknownRoundExits3(t *testing.T) {
	deps := testDeps()
	dir := workerProject(t, `{}`)
	seedLedger(t, dir)
	if code, _, _ := rotaInWith(t, deps, dir, "round", "summary", "--round", "9"); code != 3 {
		t.Errorf("exit %d, want 3", code)
	}
}
