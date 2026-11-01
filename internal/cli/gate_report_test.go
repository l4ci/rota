package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
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
			gittest.Run(t, wt, "add", "feature.txt")
			gittest.Run(t, wt, "commit", "-q", "-m", "Implement [T01]")
			head := gittest.Run(t, wt, "rev-parse", "--short", "HEAD")
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
