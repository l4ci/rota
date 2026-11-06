package testaudit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTelemetryThresholdsAndUnknownEvidence(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".rota")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	put := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := ReadTelemetry(root, Checkpoint{}, 10, 2)
	if r.LastFullGateSeconds != nil || r.MergedPRsSinceAudit != nil || r.SuggestMaintenance || len(r.Unavailable) < 3 {
		t.Fatalf("unknown evidence became a number: %+v", r)
	}
	put("ledger.jsonl", `{"kind":"verification","ts":"2026-11-06T10:00:00Z","detail":{"tier":"test.full","wallSeconds":12,"suites":[{"command":"unit","wallSeconds":4,"exitCode":0},{"command":"browser","wallSeconds":8,"exitCode":1}]}}
{"kind":"merge","pr":"#1","ts":"2026-11-06T09:00:00Z"}
{"kind":"merge","pr":"#1","ts":"2026-11-06T10:00:00Z"}
{"kind":"merge","pr":"#2","ts":"2026-11-06T10:00:00Z"}
`)
	put("flakes.jsonl", `{"check":"unit","tests":["x","y"],"tree":"abc","prs":["#1"],"round":1,"ts":"2026-11-06T10:00:00Z","first":{"exitCode":1},"retry":{"exitCode":0,"wallSeconds":2}}`)
	put("gate-audit.jsonl", `{"gate":"merge-approval","ts":"2026-11-06T10:00:00Z"}`)
	r = ReadTelemetry(root, Checkpoint{}, 10, 2)
	if !r.Complete || !r.SuggestMaintenance || len(r.Reasons) != 2 || *r.MergedPRsSinceAudit != 2 || len(r.Suites) != 2 || *r.LastFullGateSeconds != 12 || *r.Suites[1].Flakes != 1 {
		t.Fatalf("measured fixture: %+v", r)
	}
	at, _ := time.Parse(time.RFC3339, "2026-11-06T09:30:00Z")
	r = ReadTelemetry(root, Checkpoint{at}, 12, 2)
	if r.SuggestMaintenance || *r.MergedPRsSinceAudit != 1 {
		t.Fatalf("strict duration threshold or duplicate merge triggered: %+v", r)
	}
	at = at.Add(time.Hour)
	r = ReadTelemetry(root, Checkpoint{at}, 1, 1)
	if r.SuggestMaintenance || *r.MergedPRsSinceAudit != 0 {
		t.Fatalf("pre-audit evidence triggered again: %+v", r)
	}
	put("ledger.jsonl", "{broken\n")
	r = ReadTelemetry(root, Checkpoint{}, 1, 1)
	if r.Complete || r.SuggestMaintenance || r.MergedPRsSinceAudit != nil {
		t.Fatalf("corrupt evidence accepted: %+v", r)
	}
}

func TestAuditCheckpointMonotonicAndMalformed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "audit.json")
	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if err := SaveCheckpoint(p, at); err != nil {
		t.Fatal(err)
	}
	if err := SaveCheckpoint(p, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	c, err := ReadCheckpoint(p)
	if err != nil || !c.AuditedAt.Equal(at) {
		t.Fatalf("checkpoint moved backwards: %+v %v", c, err)
	}
	if err := os.WriteFile(p, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCheckpoint(p); err == nil {
		t.Fatal("invalid checkpoint accepted")
	}
	if err := SaveCheckpoint(p, at); err == nil {
		t.Fatal("invalid checkpoint overwritten")
	}
}

func TestMalformedMeasurementInvalidatesDurations(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".rota")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	body := `{"kind":"verification","ts":"2026-11-06T10:00:00Z","detail":{"tier":"test.full","wallSeconds":12,"suites":[{"command":"unit","wallSeconds":-4,"exitCode":0}]}}`
	if err := os.WriteFile(filepath.Join(dir, "ledger.jsonl"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	r := ReadTelemetry(root, Checkpoint{}, 1, 0)
	if r.Complete || r.LastFullGateSeconds != nil || r.SuggestMaintenance || !strings.Contains(strings.Join(r.Unavailable, " "), "malformed event") {
		t.Fatalf("malformed timing used: %+v", r)
	}
}
