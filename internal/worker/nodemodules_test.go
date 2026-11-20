package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func TestNodeModulesPointerSymlinkIntoWorktree(t *testing.T) {
	root, wt := t.TempDir(), filepath.Join(t.TempDir(), "w7")
	nm := filepath.Join(root, "node_modules")
	if err := os.MkdirAll(filepath.Join(nm, "@scope"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt, "node_modules", ".pnpm", "vite"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := NodeModulesPointer(root, wt); got != "" {
		t.Fatalf("clean node_modules flagged: %q", got)
	}
	other := t.TempDir()
	os.Symlink(other, filepath.Join(nm, "fine"))
	if got, _ := NodeModulesPointer(root, wt); got != "" {
		t.Fatalf("unrelated link flagged: %q", got)
	}
	os.Symlink(filepath.Join(wt, "node_modules", ".pnpm", "vite"), filepath.Join(nm, "@scope", "vite"))
	if got, _ := NodeModulesPointer(root, wt); !strings.Contains(got, "@scope") {
		t.Fatalf("scoped link into the worktree not found: %q", got)
	}
}

func TestNodeModulesPointerRelativeLinkAndHoistDir(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, ".worktrees", "w7")
	hoist := filepath.Join(root, "node_modules", ".pnpm", "node_modules")
	os.MkdirAll(hoist, 0o755)
	os.MkdirAll(filepath.Join(wt, "x"), 0o755)
	os.Symlink("../../../.worktrees/w7/x", filepath.Join(hoist, "x"))
	if got, _ := NodeModulesPointer(root, wt); got == "" {
		t.Fatal("relative link in .pnpm/node_modules not found")
	}
}

func TestNodeModulesPointerVirtualStoreDir(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(t.TempDir(), "w7")
	os.MkdirAll(filepath.Join(root, "node_modules"), 0o755)
	os.MkdirAll(wt, 0o755)
	yaml := "layoutVersion: 5\nvirtualStoreDir: " + filepath.Join(wt, "node_modules", ".pnpm") + "\n"
	os.WriteFile(filepath.Join(root, "node_modules", ".modules.yaml"), []byte(yaml), 0o644)
	if got, _ := NodeModulesPointer(root, wt); !strings.Contains(got, "virtualStoreDir") {
		t.Fatalf("virtualStoreDir into the worktree not found: %q", got)
	}
	os.WriteFile(filepath.Join(root, "node_modules", ".modules.yaml"), []byte("virtualStoreDir: .pnpm\n"), 0o644)
	if got, _ := NodeModulesPointer(root, wt); got != "" {
		t.Fatalf("relative virtualStoreDir flagged: %q", got)
	}
}

func TestNodeModulesPointerNoNodeModules(t *testing.T) {
	if got, _ := NodeModulesPointer(t.TempDir(), t.TempDir()); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestPoolReapKeepsTheSlotWhenNodeModulesPointsIn(t *testing.T) {
	b := gittest.Project(t, `{}`)
	goInit(t, b, InitOpts{Slots: 2, Base: "main"})
	w2 := filepath.Join(b, ".worktrees", "w2")
	os.MkdirAll(filepath.Join(b, "node_modules"), 0o755)
	os.MkdirAll(filepath.Join(w2, "node_modules", "vite"), 0o755)
	os.Symlink(filepath.Join(w2, "node_modules", "vite"), filepath.Join(b, "node_modules", "vite"))

	reaped, err := Env{}.Reap(b, []string{"w1", "w2"}, false)
	if err == nil || !strings.Contains(err.Error(), "pnpm install --offline --frozen-lockfile") || !strings.Contains(err.Error(), "w2") {
		t.Fatalf("want an error naming w2 and the fix, got %v (reaped %v)", err, reaped)
	}
	if _, serr := os.Stat(w2); serr != nil {
		t.Error("w2 worktree was removed")
	}
	if LoadRegistryTolerant(b).Slot("w2") == nil {
		t.Error("w2 dropped from the registry")
	}
	if LoadRegistryTolerant(b).Slot("w1") != nil {
		t.Error("w1 has no pointer and should be reaped")
	}
}

// An I/O error while looking is not "nothing points in": reap is fail-closed.
func TestNodeModulesPointerUnreadableHolds(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	root, wt := t.TempDir(), filepath.Join(t.TempDir(), "w7")
	nm := filepath.Join(root, "node_modules")
	if err := os.MkdirAll(filepath.Join(nm, "vite"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nm, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(nm, 0o755) })
	if got, err := NodeModulesPointer(root, wt); err == nil {
		t.Fatalf("unreadable node_modules not held: %q", got)
	}
}

// A link whose target is a symlink into the worktree resolves there too.
func TestNodeModulesPointerResolvedLink(t *testing.T) {
	root, wt := t.TempDir(), filepath.Join(t.TempDir(), "w7")
	nm := filepath.Join(root, "node_modules")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	hop := filepath.Join(t.TempDir(), "hop")
	if err := os.Symlink(filepath.Join(wt, "pkg"), hop); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hop, filepath.Join(nm, "vite")); err != nil {
		t.Fatal(err)
	}
	if got, _ := NodeModulesPointer(root, wt); got == "" {
		t.Fatal("link resolving into the worktree not flagged")
	}
}
