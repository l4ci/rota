package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorkerDoneNeedsACleanPushedBranch: done is the worker's last action, so
// a dirty worktree, an unpushed branch or a HEAD past the pushed branch is refused (#1529).
func TestWorkerDoneNeedsACleanPushedBranch(t *testing.T) {
	t.Run("clean and pushed passes", func(t *testing.T) {
		dir := doneProject(t, "")
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("dirty worktree refuses", func(t *testing.T) {
		dir := doneProject(t, "")
		os.WriteFile(filepath.Join(dir, "late.txt"), []byte("x"), 0o644)
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json")
		if code != 4 || data(t, out)["dirty"] != true || slotState(t, dir) == "done" {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
}
