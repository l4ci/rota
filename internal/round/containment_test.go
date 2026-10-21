package round

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/worker"
)

// A checkout reached through a symlink is the same worktree: every round
// containment decision must agree with worker.PathContains.
func TestContainmentFollowsSymlinks(t *testing.T) {
	real := t.TempDir()
	wt := filepath.Join(real, worker.WorktreeRoot, "ben")
	if err := os.MkdirAll(filepath.Join(wt, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "checkout")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(link, worker.WorktreeRoot, "ben", "sub")

	t.Run("matchAgent", func(t *testing.T) {
		agents := []host.Agent{{Tab: "w1:t1", Cwd: linked}}
		if got := matchAgent(agents, "ben", "", wt); got != 0 {
			t.Errorf("matchAgent via symlinked cwd = %d, want 0", got)
		}
	})
	t.Run("inExternal", func(t *testing.T) {
		rows := []*Row{{Name: "ben", HostState: ExternalHost}}
		views := map[string]*view{"ben": {worktree: wt}}
		if !inExternal(rows, views, linked) {
			t.Error("inExternal via symlinked cwd = false, want true")
		}
	})
	t.Run("rootAgents", func(t *testing.T) {
		if !inWorktreeRoot(real, linked) {
			t.Error("symlinked cwd under .worktrees must count as inside")
		}
		if inWorktreeRoot(real, filepath.Join(real, "elsewhere")) {
			t.Error("cwd outside .worktrees counted as inside")
		}
	})
}
