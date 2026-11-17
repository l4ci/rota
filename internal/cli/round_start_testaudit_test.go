package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const slowGateLedger = `{"kind":"verification","ts":"2026-11-06T10:00:00Z","detail":{"tier":"test.full","wallSeconds":12,"suites":[{"command":"unit","wallSeconds":12,"exitCode":0}]}}` + "\n"

// auditStartProject is a round-start project whose recorded full gate (12s)
// exceeds test.audit.maxFullGateSeconds (10), so a suggestion is due, unless
// quiet leaves the ledger empty.
func auditStartProject(t *testing.T, threshold string, due bool) string {
	t.Helper()
	dir := orchProject(t, false)
	os.WriteFile(filepath.Join(dir, ".rota", "BACKLOG.md"), []byte("# TODO\n\n## Bugs\n\n## Features\n\n## Tasks\n\n## Completed\n"), 0o644)
	cfg := `{"git":{"baseBranch":"feat/x"},"test":{"audit":{"maxFullGateSeconds":` + threshold + `}}}`
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(cfg), 0o644)
	if due {
		os.WriteFile(filepath.Join(dir, ".rota", "ledger.jsonl"), []byte(slowGateLedger), 0o644)
	}
	return dir
}

func startAudit(t *testing.T, dir string, json bool) (string, string) {
	t.Helper()
	args := []string{"round", "start", "--holder-pid", strconv.Itoa(os.Getpid()), "--base", "feat/x", "--slots", "1"}
	if json {
		args = append([]string{"--json"}, args...)
	}
	code, out, errOut := rotaStdin(t, dir, "", args...)
	if code != 0 {
		t.Fatalf("round start: %d %s %s", code, out, errOut)
	}
	return out, errOut
}

func TestRoundStartReportsTestAuditSuggestion(t *testing.T) {
	dir := auditStartProject(t, "10", true)
	out, _ := startAudit(t, dir, true)
	var m struct {
		Data struct {
			TestAudit *struct {
				Suggest *bool    `json:"suggestMaintenance"`
				Reasons []string `json:"reasons"`
			} `json:"testAudit"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	ta := m.Data.TestAudit
	if ta == nil || ta.Suggest == nil || !*ta.Suggest || len(ta.Reasons) != 1 || !strings.Contains(ta.Reasons[0], "exceeds 10s") {
		t.Fatalf("data.testAudit = %+v in %s", ta, out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "rota", "test-audit.json")); !os.IsNotExist(err) {
		t.Fatalf("round start wrote an audit checkpoint: %v", err)
	}
}

func TestRoundStartTestAuditTextLineOnlyWhenDue(t *testing.T) {
	out, _ := startAudit(t, auditStartProject(t, "10", true), false)
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "test-audit\t") {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "exceeds 10s") {
		t.Fatalf("want one test-audit line with the reasons, got %q in %s", lines, out)
	}
	quiet, _ := startAudit(t, auditStartProject(t, "10", false), false)
	if strings.Contains(quiet, "test-audit") {
		t.Fatalf("no suggestion due, yet the output names it: %s", quiet)
	}
}

func TestRoundStartTestAuditReadFailureIsAWarning(t *testing.T) {
	dir := auditStartProject(t, "10", true)
	cd := filepath.Join(dir, ".git", "rota")
	os.MkdirAll(cd, 0o755)
	os.WriteFile(filepath.Join(cd, "test-audit.json"), []byte("{broken"), 0o644)
	out, errOut := startAudit(t, dir, false)
	if strings.Contains(out, "test-audit\t") {
		t.Fatalf("an unreadable checkpoint must not suggest: %s", out)
	}
	if !strings.Contains(errOut, "test audit") {
		t.Fatalf("an unreadable checkpoint must warn, got stderr %q", errOut)
	}
}
