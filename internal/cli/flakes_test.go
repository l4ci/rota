package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

const recoveryRow = `{"check":"go test ./...","tests":["TestLoad"],"tree":"abc123","prs":["#7"],"round":2,"ts":"2026-10-27T12:00:00Z","first":{"output":"--- FAIL: TestLoad (0s)\nFAIL\n","exitCode":1},"retry":{"output":"ok\n","exitCode":0}}` + "\n"

func TestFlakeLedgerCommandsAndRoundCount(t *testing.T) {
	dir := workerProject(t, `{}`)
	seedLedger(t, dir)
	p := filepath.Join(dir, ".rota", "flakes.jsonl")
	// One record belongs to another round; repeated test names still count
	// once per recovery, and a recovery with two tests counts once per round.
	rows := recoveryRow + recoveryRow + strings.Replace(recoveryRow, `"round":2`, `"round":1`, 1) + strings.Replace(recoveryRow, `["TestLoad"]`, `["TestOther","TestOther","TestThird"]`, 1)
	if err := os.WriteFile(p, []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := rotaInWith(t, testDeps(), dir, "test", "flakes", "--json")
	if code != 0 {
		t.Fatalf("test flakes exit %d: %s", code, out)
	}
	d := data(t, out)
	flakes, _ := d["flakes"].([]any)
	if len(flakes) != 3 || d["records"] != float64(4) {
		t.Fatalf("ranking: %v", d)
	}
	first := flakes[0].(map[string]any)
	if first["test"] != "TestLoad" || first["count"] != float64(3) {
		t.Fatalf("worst flake must rank first: %v", first)
	}
	code, out, _ = rotaInWith(t, testDeps(), dir, "round", "summary", "--round", "2", "--json")
	if code != 0 {
		t.Fatalf("summary exit %d: %s", code, out)
	}
	metric := data(t, out)["metrics"].(map[string]any)["flakes"]
	m, ok := metric.(map[string]any)
	if !ok || m["count"] != float64(3) || m["unavailable"] != nil {
		t.Fatalf("one count per recovery in round 2: %v", metric)
	}
	obj, _, ok := windDownSummary(dir, 2)
	if !ok {
		t.Fatal("wind-down summary missing")
	}
	metrics, _ := obj.Get("metrics")
	flaky, _ := metrics.(*jsonx.Object).Get("flakes")
	count, _ := flaky.(*jsonx.Object).Get("count")
	if count != 3 {
		t.Fatalf("wind-down omitted flakes: %v", obj)
	}
}

func TestFlakeLedgerMissingEmptyAndMalformed(t *testing.T) {
	dir := workerProject(t, `{}`)
	seedLedger(t, dir)
	p := filepath.Join(dir, ".rota", "flakes.jsonl")
	for _, tc := range []struct {
		name, body string
		missing    bool
		wantCode   int
		wantCount  any
	}{
		{"missing", "", true, 0, nil},
		{"empty", "", false, 0, float64(0)},
		{"malformed", "{broken\n", false, 5, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.missing {
				if err := os.WriteFile(p, []byte(tc.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			code, out, _ := rotaInWith(t, testDeps(), dir, "test", "flakes", "--json")
			if code != tc.wantCode {
				t.Fatalf("exit=%d, want %d: %s", code, tc.wantCode, out)
			}
			code, out, _ = rotaInWith(t, testDeps(), dir, "round", "summary", "--json")
			if code != 0 {
				t.Fatalf("summary exit=%d: %s", code, out)
			}
			metric := data(t, out)["metrics"].(map[string]any)["flakes"]
			m, ok := metric.(map[string]any)
			if !ok || m["count"] != tc.wantCount || ((m["unavailable"] == nil) != (tc.wantCount != nil)) {
				t.Fatalf("metric=%v", metric)
			}
		})
	}
}
