package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/ledger"
)

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
	for _, want := range []string{"Round 2", "quota share", "QUOTA SHARE", "25%", "ben", "work", "Gate audit"} {
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
