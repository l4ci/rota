package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

// Exercise the verb's shared runGate path: a refusal returns before post-gate
// bookkeeping, but still needs the same durable report as a successful merge.
func TestGateReportFileOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, checks, verdict string
		checkOnly             bool
		code                  int
	}{
		{name: "merged", checks: `["test -f feature.txt"]`, verdict: worker.GatePass},
		{name: "refused", checks: `[]`, verdict: worker.GateNoVerify, code: 4},
		{name: "check only", checks: `["test -f feature.txt"]`, verdict: worker.GateFresh, checkOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := workerProject(t, fmt.Sprintf(`{"ship":{"review":"none"},"test":{"full":%s}}`, tc.checks))
			write(t, filepath.Join(dir, ".rota", "BACKLOG.md"), "# TODO\n\n## Tasks\n\n- **[T01] [Minor] Report target.** Evidence\n\n## Completed\n")
			const original = "## Problem\n\nThe gate report must be durable.\n"
			write(t, filepath.Join(dir, ".rota", "tasks", "T01.md"), original)
			if code, _, errs := rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main"); code != 0 {
				t.Fatalf("pool: %d %s", code, errs)
			}
			if _, err := worker.UpdateSlot(dir, "w1", func(s *worker.Slot) { s.SetTask("T01") }); err != nil {
				t.Fatal(err)
			}
			wt := filepath.Join(dir, ".worktrees", "w1")
			write(t, filepath.Join(wt, "feature.txt"), "feature\n")
			gitT(t, wt, "add", "feature.txt")
			gitT(t, wt, "commit", "-q", "-m", "Implement [T01]")
			cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
			cmd.Dir = wt
			head, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"worker", "gate", "w1", "--base", "main", "--no-assign-check", "--json"}
			if tc.checkOnly {
				args = append(args, "--check-only")
			}
			code, out, errs := rotaIn(t, dir, args...)
			d := data(t, out)
			if code != tc.code || d["verdict"] != tc.verdict {
				t.Fatalf("gate: %d %s %s", code, out, errs)
			}
			text := readTask(t, dir, "T01")
			if tc.checkOnly {
				if text != original {
					t.Fatalf("check-only wrote a report:\n%s", text)
				}
				return
			}
			sha, _ := d["sha"].(string)
			if sha == "" {
				sha = strings.TrimSpace(string(head))
			}
			marker := "<!-- rota:report gate sha=" + sha + " -->"
			t.Logf("gate %s report blocks=%d", tc.verdict, strings.Count(text, marker))
			if strings.Count(text, marker) != 1 || !strings.Contains(text, "## Reports\n") || !strings.Contains(text, "Outcome: **"+tc.verdict+"**") {
				t.Fatalf("gate did not leave one marked outcome report:\n%s", text)
			}
			if tc.verdict == worker.GatePass {
				if !strings.Contains(text, "`test -f feature.txt`") {
					t.Fatalf("merge report omitted the check that ran:\n%s", text)
				}
				if _, err := os.Stat(filepath.Join(dir, "feature.txt")); err != nil {
					t.Fatal("gate reported pass without merging the feature")
				}
			} else if !strings.Contains(text, "### Refusal\n") || !strings.Contains(text, "test.full is empty") {
				t.Fatalf("refusal report omitted the reason:\n%s", text)
			}
		})
	}
}

func TestGateReportPRMergeAndRefusal(t *testing.T) {
	dir := fileDeferredRepo(t)
	write(t, filepath.Join(dir, ".rota", "config.json"), `{"forge":{"provider":"github"}}`)
	write(t, filepath.Join(dir, ".rota", "BACKLOG.md"), "# TODO\n\n## Tasks\n\n- **[T01] [Minor] Gate target.** Evidence\n")
	var reports []string
	f := &forge{answer: func(_ string, args []string) (string, string, int) {
		call := strings.Join(args, " ")
		if strings.HasPrefix(call, "pr view 20 ") {
			return `{"headRefName":"feat/x","body":"Resolves [T01]"}`, "", 0
		}
		if strings.Contains(call, "issues/20/comments") {
			if slices.Contains(args, "POST") {
				for _, arg := range args {
					if strings.HasPrefix(arg, "body=") {
						reports = append(reports, strings.TrimPrefix(arg, "body="))
					}
				}
				return `{"id":1}`, "", 0
			}
			return "[]", "", 0
		}
		return "[]", "", 0
	}}
	deps := useForge(t, f)
	deps.Getwd = func() (string, error) { return dir, nil }
	var warnings bytes.Buffer
	c := &Ctx{Deps: deps, Stderr: &warnings}
	for _, r := range []worker.GateResult{
		{Branch: "feat/x", PR: "20", Issue: "T01", SHA: "abc1234", Verdict: worker.GatePass, Verified: []string{"go test ./internal/verdict"}},
		{Branch: "feat/x", PR: "20", Issue: "T01", SHA: "def5678", Verdict: worker.GateNoVerify, Err: "test.full is empty"},
	} {
		postGateReport(c, dir, r)
	}
	if len(reports) != 2 || !strings.Contains(reports[0], "<!-- rota:report gate sha=abc1234 -->") || !strings.Contains(reports[1], "Outcome: **no-verify**") || !strings.Contains(reports[1], "test.full is empty") {
		t.Fatalf("PR reports: %v", reports)
	}
	if warnings.Len() != 0 {
		t.Fatalf("unexpected pending report: %s", &warnings)
	}
	if text := readTask(t, dir, "T01"); strings.Count(text, "<!-- rota:report gate sha=") != 2 {
		t.Fatalf("linked item's gate reports: %s", text)
	}
}
