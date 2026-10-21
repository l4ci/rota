package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.email=a@b", "-c", "user.name=n"}, args...)...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// TestWorkerDoneNeedsACleanPushedBranch: done is the worker's last action, so
// a dirty worktree, an unpushed branch or a HEAD past the pushed branch is refused (#1529).
func TestWorkerDoneNeedsACleanPushedBranch(t *testing.T) {
	t.Run("clean and pushed passes", func(t *testing.T) {
		dir := doneProject(t, "")
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
	t.Run("never pushed refuses", func(t *testing.T) {
		dir := doneProject(t, "")
		runGit(t, dir, "update-ref", "-d", "refs/remotes/origin/feat/x")
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json")
		if code != 4 || data(t, out)["unpushed"] != true || slotState(t, dir) == "done" {
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
	t.Run("commits past the pushed branch refuse", func(t *testing.T) {
		dir := doneProject(t, "")
		runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "late")
		code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json")
		if code != 4 || data(t, out)["unpushed"] != true || !strings.Contains(out, "git push") {
			t.Fatalf("exit %d: %s", code, out)
		}
		runGit(t, dir, "push", "-q", "origin", "feat/x")
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--json"); code != 0 {
			t.Fatalf("after push: exit %d: %s", code, out)
		}
	})
	t.Run("failed outcome skips the check", func(t *testing.T) {
		dir := doneProject(t, "")
		os.WriteFile(filepath.Join(dir, "late.txt"), []byte("x"), 0o644)
		f := filepath.Join(t.TempDir(), "s")
		os.WriteFile(f, []byte("a\nb\nc\n"), 0o644)
		if code, out, _ := rotaIn(t, dir, "worker", "done", "ben", "--outcome", "failed", "--summary-file", f, "--json"); code != 0 {
			t.Fatalf("exit %d: %s", code, out)
		}
	})
}
