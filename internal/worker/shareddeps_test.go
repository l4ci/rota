package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// sharedProject is a project whose main checkout carries node_modules and a
// lockfile, with sharing configured.
func sharedProject(t *testing.T, setup string) string {
	t.Helper()
	cfg := `{"round":{"sharedDirs":["node_modules"]` + setup + `}}`
	b := newProject(t, cfg)
	os.WriteFile(filepath.Join(b, ".gitignore"), []byte(".worktrees/\nnode_modules/\n"), 0o644)
	os.WriteFile(filepath.Join(b, "package-lock.json"), []byte("lock-a\n"), 0o644)
	gittest.Run(t, b, "add", ".gitignore", "package-lock.json")
	gittest.Run(t, b, "commit", "-q", "-m", "lock")
	os.MkdirAll(filepath.Join(b, "node_modules", "left-pad"), 0o755)
	return b
}

func linkTarget(t *testing.T, b, slot string) string {
	t.Helper()
	got, err := os.Readlink(filepath.Join(b, ".worktrees", slot, "node_modules"))
	if err != nil {
		return ""
	}
	return got
}

func TestSharedDirsLinkedOnPoolInitWithoutInstall(t *testing.T) {
	b := sharedProject(t, `,"envSetup":"npm ci"`)
	r := &setupRecorder{}
	res, err := Env{Shell: r.shell}.PoolInit(bg, b, InitOpts{Slots: 1, Base: "main"}, &Accounts{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := linkTarget(t, b, "w1"), filepath.Join(b, "node_modules"); got != want {
		t.Errorf("node_modules link = %q, want %q", got, want)
	}
	if len(r.runs) != 0 {
		t.Errorf("envSetup ran %v, want none while every directory is shared", r.runs)
	}
	if len(res.Shared) != 1 || res.Shared[0].Action != "linked" || res.Shared[0].Dir != "node_modules" || res.Shared[0].Slot != "w1" {
		t.Errorf("Shared = %+v, want one linked node_modules row for w1", res.Shared)
	}
	if out := gittest.Run(t, filepath.Join(b, ".worktrees", "w1"), "status", "--porcelain"); out != "" {
		t.Errorf("worktree is dirty after linking: %q", out)
	}
}

func TestSharedDirsSkippedWhenLockfileDiffers(t *testing.T) {
	b := sharedProject(t, `,"envSetup":"npm ci"`)
	r := &setupRecorder{}
	env := Env{Shell: r.shell}.WithDefaults()
	if _, err := env.PoolInit(bg, b, InitOpts{Slots: 1, Base: "main"}, &Accounts{}); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(b, ".worktrees", "w1")
	os.WriteFile(filepath.Join(wt, "package-lock.json"), []byte("lock-b\n"), 0o644)
	res, err := env.slotDeps(bg, b, "w1", wt, ParseDepsConfig(env.Config(b)), 20000, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := linkTarget(t, b, "w1"); got != "" {
		t.Errorf("stale link kept (%q), want it removed so the slot installs its own", got)
	}
	if len(r.runs) != 1 {
		t.Errorf("envSetup runs = %v, want one", r.runs)
	}
	if len(res) != 1 || res[0].Action != "unlinked" {
		t.Errorf("rows = %+v, want one unlinked row", res)
	}
}

func TestSharedDirsRelinkedWhenTargetVanished(t *testing.T) {
	b := sharedProject(t, ``)
	env := Env{Shell: (&setupRecorder{}).shell}.WithDefaults()
	if _, err := env.PoolInit(bg, b, InitOpts{Slots: 1, Base: "main"}, &Accounts{}); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(b, ".worktrees", "w1")
	rows, _ := env.slotDeps(bg, b, "w1", wt, ParseDepsConfig(env.Config(b)), 20000, 100)
	if len(rows) != 1 || rows[0].Action != "kept" {
		t.Errorf("rows = %+v, want kept", rows)
	}
	os.Remove(filepath.Join(wt, "node_modules"))
	rows, _ = env.slotDeps(bg, b, "w1", wt, ParseDepsConfig(env.Config(b)), 20000, 100)
	if len(rows) != 1 || rows[0].Action != "linked" || linkTarget(t, b, "w1") == "" {
		t.Errorf("rows = %+v, want linked again", rows)
	}
}

func TestSharedDirsSkippedWhenMissingInMainOrUnsafe(t *testing.T) {
	b := sharedProject(t, ``)
	env := Env{Shell: (&setupRecorder{}).shell}.WithDefaults()
	if _, err := env.PoolInit(bg, b, InitOpts{Slots: 1, Base: "main"}, &Accounts{}); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(b, ".worktrees", "w1")
	os.Remove(filepath.Join(wt, "node_modules"))
	rows, _ := env.shareDeps(b, "w1", wt, []string{"../escape", "/abs", ".venv"})
	for _, r := range rows {
		if r.Action != "skipped" {
			t.Errorf("row %+v, want skipped", r)
		}
	}
	if len(rows) != 3 {
		t.Errorf("rows = %+v, want 3", rows)
	}
}

func TestSharedDirsKeepsRealDirectory(t *testing.T) {
	b := sharedProject(t, ``)
	env := Env{Shell: (&setupRecorder{}).shell}.WithDefaults()
	if _, err := env.PoolInit(bg, b, InitOpts{Slots: 1, Base: "main"}, &Accounts{}); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(b, ".worktrees", "w1")
	os.Remove(filepath.Join(wt, "node_modules"))
	os.MkdirAll(filepath.Join(wt, "node_modules", "own"), 0o755)
	rows, _ := env.shareDeps(b, "w1", wt, []string{"node_modules"})
	if len(rows) != 1 || rows[0].Action != "skipped" {
		t.Errorf("rows = %+v, want skipped", rows)
	}
	if _, err := os.Stat(filepath.Join(wt, "node_modules", "own")); err != nil {
		t.Errorf("the slot's own directory was touched: %v", err)
	}
}
