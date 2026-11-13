package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// tempReviewProject is a rota project (a main checkout with .rota/) and a linked
// worktree of it, with the system temp dir pinned to a separate directory.
func tempReviewProject(t *testing.T) (d *Deps, main, wt, system string) {
	t.Helper()
	main = newRepo(t, t.TempDir(), "repo", "main")
	if err := os.Mkdir(filepath.Join(main, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	wt = filepath.Join(t.TempDir(), "wt")
	gittest.Run(t, main, "worktree", "add", "-q", wt, "-b", "slot")
	system = t.TempDir()
	d = testDeps()
	d.TempDir = func() string { return system }
	return d, main, wt, system
}

func tempReviewPrepared(t *testing.T, d *Deps, dir string) string {
	t.Helper()
	o := trRunWith(t, d, dir, "", "review", "prepare", "HEAD", "--json")
	if o.code != 0 {
		t.Fatalf("prepare: rc=%d %s %s", o.code, o.stdout, o.stderr)
	}
	return envelope(t, o.stdout)["data"].(map[string]any)["path"].(string)
}

func TestReviewWorkspaceUnderProjectTemp(t *testing.T) {
	d, main, wt, _ := tempReviewProject(t)
	real, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(real, ".rota", "tmp")
	// A worker's prepare and the orchestrator's land in the one project root.
	for name, dir := range map[string]string{"main checkout": main, "linked worktree": wt} {
		path := tempReviewPrepared(t, d, dir)
		if got := filepath.Dir(filepath.Dir(path)); got != want {
			t.Fatalf("%s: workspace %s is not under the project temp root %s", name, path, want)
		}
		// git run in the exported tree must stop at the workspace, never
		// resolve to the checkout the temp root lives in.
		if top := strings.TrimSpace(gittest.Run(t, path, "rev-parse", "--show-toplevel")); top != filepath.Dir(path) {
			t.Fatalf("%s: git in the review tree resolves to %s, want the workspace %s", name, top, filepath.Dir(path))
		}
		if o := trRunWith(t, d, dir, "", "review", "cleanup", path, "--json"); o.code != 0 {
			t.Fatalf("%s: cleanup: rc=%d %s %s", name, o.code, o.stdout, o.stderr)
		}
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Fatalf("%s: workspace survived cleanup: %v", name, err)
		}
	}
	if out := gittest.Run(t, main, "status", "--porcelain", "--untracked-files=all"); out != "" {
		t.Errorf("the temp root shows in git status:\n%s", out)
	}
}

func TestReviewProbeUnderProjectTemp(t *testing.T) {
	d, main, _, system := tempReviewProject(t)
	real, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatal(err)
	}
	o := trRunWith(t, d, main, "", "review", "probe", "--json", "--", "sh", "-c", `printf %s "$HOME"`)
	if o.code != 0 {
		t.Fatalf("probe: rc=%d %s %s", o.code, o.stdout, o.stderr)
	}
	home := envelope(t, o.stdout)["data"].(map[string]any)["stdout"].(string)
	if rel, err := filepath.Rel(filepath.Join(real, ".rota", "tmp"), home); err != nil || !filepath.IsLocal(rel) {
		t.Fatalf("probe HOME %s is not under the project temp root (system temp %s)", home, system)
	}
}

// Cleanup anchors on the project temp root as the exact parent: a workspace
// with a valid ownership record anywhere else in the project is refused.
func TestReviewWorkspaceCleanupProjectAnchorIsExact(t *testing.T) {
	d, main, _, _ := tempReviewProject(t)
	path := tempReviewPrepared(t, d, main)
	ws := filepath.Dir(path)
	tmpRoot := filepath.Dir(ws)
	for name, parent := range map[string]string{
		"the .rota dir":          filepath.Dir(tmpRoot),
		"a subdir of the root":   filepath.Join(tmpRoot, "nested"),
		"the main checkout root": main,
	} {
		moved := filepath.Join(parent, filepath.Base(ws))
		if err := os.MkdirAll(parent, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(ws, moved); err != nil {
			t.Fatal(err)
		}
		// Rewrite the record so only the location is wrong.
		write(t, filepath.Join(moved, reviewWorkspaceMarker), `{"path":"`+filepath.Join(moved, "tree")+`","head":"x"}`)
		if o := trRunWith(t, d, main, "", "review", "cleanup", filepath.Join(moved, "tree"), "--json"); o.code != 4 {
			t.Fatalf("%s: cleanup accepted a workspace outside the exact root: rc=%d %s", name, o.code, o.stdout)
		}
		if _, err := os.Stat(moved); err != nil {
			t.Fatalf("%s: refused workspace was removed: %v", name, err)
		}
		if err := os.Rename(moved, ws); err != nil {
			t.Fatal(err)
		}
	}
}

// A workspace a project left in the system temp dir (before #2499, or from a
// run outside any project) stays cleanable from inside the project.
func TestReviewWorkspaceCleanupKeepsSystemTemp(t *testing.T) {
	d, main, _, system := tempReviewProject(t)
	outside := newRepo(t, t.TempDir(), "plain", "main")
	path := tempReviewPrepared(t, d, outside)
	canonical, err := filepath.EvalSymlinks(system)
	if err != nil || filepath.Dir(filepath.Dir(path)) != canonical {
		t.Fatalf("outside a project the workspace %s must fall back to the system temp %s: %v", path, system, err)
	}
	if o := trRunWith(t, d, main, "", "review", "cleanup", path, "--json"); o.code != 0 {
		t.Fatalf("cleanup of a system-temp workspace: rc=%d %s %s", o.code, o.stdout, o.stderr)
	}
}

func TestGitTmpDirPrintsProjectRootOrSystemTemp(t *testing.T) {
	d, main, wt, system := tempReviewProject(t)
	real, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(real, ".rota", "tmp")
	o := trRunWith(t, d, wt, "", "git", "tmp-dir", "--json")
	if o.code != 0 {
		t.Fatalf("tmp-dir: rc=%d %s %s", o.code, o.stdout, o.stderr)
	}
	data := envelope(t, o.stdout)["data"].(map[string]any)
	if data["path"] != want || data["project"] != true {
		t.Fatalf("tmp-dir from a linked worktree = %v, want %s in the project", data, want)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatalf("tmp-dir did not create the root: %v", err)
	}
	plain := newRepo(t, t.TempDir(), "plain", "main")
	o = trRunWith(t, d, plain, "", "git", "tmp-dir", "--json")
	canonical, _ := filepath.EvalSymlinks(system)
	data = envelope(t, o.stdout)["data"].(map[string]any)
	if o.code != 0 || data["path"] != canonical || data["project"] != false {
		t.Fatalf("tmp-dir outside a rota project = rc %d %v, want the system temp %s", o.code, data, canonical)
	}
}
