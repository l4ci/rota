package cli

import (
	"encoding/json"
	"github.com/l4ci/rota/internal/gittest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/worker"
)

func TestWorkerGateTrainAssignWarning(t *testing.T) {
	for _, verb := range []string{"gate", "train"} {
		for _, tc := range []struct {
			name                            string
			candidate, quiet, preview, busy bool
		}{
			{name: "ready candidate", candidate: true},
			{name: "no candidate"},
			{name: "suppressed", candidate: true, quiet: true},
			{name: "preview", candidate: true, preview: true},
			{name: "no free slot", candidate: true, busy: true},
		} {
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
				dir := workerProject(t, `{"round":{"roster":["w1","w2"],"slots":2,"scope":"open"},"ship":{"review":"none"},"review":{"fileDeferred":"minor"},"test":{"full":["true"]}}`)
				if code, _, errOut := rotaIn(t, dir, "worker", "pool", "init", "--slots", "2", "--base", "main"); code != 0 {
					t.Fatalf("pool: %d %s", code, errOut)
				}
				write(t, filepath.Join(dir, ".rota", "BACKLOG.md"), "# TODO\n\n## Tasks\n\n## Completed\n")
				write(t, filepath.Join(dir, ".rota", "counters.json"), "{}\n")
				if tc.candidate {
					body := writeBody(t, t.TempDir(), `{"findings":[{"severity":"minor","title":"rename the helper"}]}`)
					if code, _, errOut := rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77"); code != 0 {
						t.Fatalf("candidate: %d %s", code, errOut)
					}
				}
				wt := filepath.Join(dir, ".worktrees", "w1")
				write(t, filepath.Join(wt, "feature.txt"), "feature")
				gittest.Run(t, wt, "add", "feature.txt")
				gittest.Run(t, wt, "commit", "-q", "-m", "feature")
				if _, err := worker.UpdateSlot(dir, "w1", func(s *worker.Slot) { s.MarkState("busy", "") }); err != nil {
					t.Fatal(err)
				}
				if tc.busy {
					if _, err := worker.UpdateSlot(dir, "w2", func(s *worker.Slot) { s.MarkState("busy", "") }); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{"--json", "worker", verb, "w1", "--base", "main"}
				if tc.quiet {
					args = append(args, "--no-assign-check")
				}
				if tc.preview {
					if verb == "gate" {
						args = append(args, "--check-only")
					} else {
						args = append(args, "--plan")
					}
				}
				code, out, stderr := rotaIn(t, dir, args...)
				if code != 0 {
					t.Fatalf("run: %d %s %s", code, out, stderr)
				}
				want := tc.candidate && !tc.quiet && !tc.preview && !tc.busy
				const hint = "assign first: rota round assign T01 --agent w2"
				if got := strings.Contains(stderr, hint); got != want {
					t.Errorf("stderr warning = %v, want %v: %s", got, want, stderr)
				}
				var env struct{ Warnings []string }
				if err := json.Unmarshal([]byte(out), &env); err != nil {
					t.Fatal(err)
				}
				if got := strings.Contains(strings.Join(env.Warnings, "\n"), hint); got != want {
					t.Errorf("JSON warning = %v, want %v: %v", got, want, env.Warnings)
				}
			})
		}
	}
}
