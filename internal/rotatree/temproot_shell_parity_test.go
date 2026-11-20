package rotatree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
)

// test/gate.sh needs a temp root before it has built rota, so test/lib/project-tmp.sh
// keeps a shell copy of the TempRootVia rule (#2499). This pins the copy to the
// Go rule: both run over the same fixture shapes and must name the same root
// (or none) and leave the same mode and .gitignore behind (#3579).
func TestProjectTmpShellMatchesTempRootVia(t *testing.T) {
	script, err := filepath.Abs(filepath.Join("..", "..", "test", "lib", "project-tmp.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("no shell copy of the temp-root rule: %v", err)
	}
	mkdir := func(t *testing.T, p string) {
		t.Helper()
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := func(t *testing.T, target, p string) {
		t.Helper()
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	// Each fixture returns the directory both rules are asked about.
	fixtures := map[string]func(t *testing.T) string{
		"plain checkout": func(t *testing.T) string {
			repo := gittest.NewRepo(t, "main")
			mkdir(t, filepath.Join(repo, ".rota"))
			return repo
		},
		"plain checkout, tmp exists": func(t *testing.T) string {
			repo := gittest.NewRepo(t, "main")
			mkdir(t, filepath.Join(repo, ".rota", "tmp"))
			return repo
		},
		"linked worktree": func(t *testing.T) string {
			repo := gittest.NewRepo(t, "main")
			mkdir(t, filepath.Join(repo, ".rota"))
			wt := filepath.Join(t.TempDir(), "wt")
			gittest.Run(t, repo, "worktree", "add", "-q", wt, "-b", "other")
			return wt
		},
		"subdirectory": func(t *testing.T) string {
			repo := gittest.NewRepo(t, "main")
			mkdir(t, filepath.Join(repo, ".rota"))
			mkdir(t, filepath.Join(repo, "sub"))
			return filepath.Join(repo, "sub")
		},
		"no .rota": func(t *testing.T) string { return gittest.NewRepo(t, "main") },
		"symlinked .rota": func(t *testing.T) string {
			repo := gittest.NewRepo(t, "main")
			elsewhere := t.TempDir()
			mkdir(t, filepath.Join(elsewhere, "tmp"))
			link(t, elsewhere, filepath.Join(repo, ".rota"))
			return repo
		},
		"symlinked .rota/tmp": func(t *testing.T) string {
			repo := gittest.NewRepo(t, "main")
			mkdir(t, filepath.Join(repo, ".rota"))
			link(t, t.TempDir(), filepath.Join(repo, ".rota", "tmp"))
			return repo
		},
		"bare repository": func(t *testing.T) string {
			repo := gittest.NewRepo(t, "main")
			bare := filepath.Join(t.TempDir(), "r.git")
			gittest.Run(t, repo, "clone", "-q", "--bare", repo, bare)
			return bare
		},
		"outside a repository": func(t *testing.T) string { return t.TempDir() },
	}
	type outcome struct {
		root      string // relative to the fixture's own checkout, "" for none
		mode      os.FileMode
		gitignore string
	}
	observe := func(t *testing.T, base, root string, ok bool) outcome {
		t.Helper()
		if !ok {
			return outcome{}
		}
		real, err := filepath.EvalSymlinks(base)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(real, root)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(root)
		if err != nil {
			t.Fatalf("root %s not created: %v", root, err)
		}
		ign, _ := os.ReadFile(filepath.Join(root, IgnoreFile))
		return outcome{rel, fi.Mode().Perm(), string(ign)}
	}
	for name, build := range fixtures {
		t.Run(name, func(t *testing.T) {
			// Two identical fixtures, so neither rule sees what the other created.
			dirGo, dirSh := build(t), build(t)
			root, ok, err := TempRootVia(context.Background(), git.Exec, dirGo)
			if err != nil {
				t.Fatal(err)
			}
			// The fixture's checkout is the dir's repo root; for a worktree or
			// subdirectory the main checkout differs, so compare absolute-free
			// shapes via the suffix under .rota.
			goOut := observe(t, filepath.Dir(filepath.Dir(orDot(root))), root, ok)

			cmd := exec.Command("bash", "-c", `. "$1" && project_tmp "$2"`, "bash", script, dirSh)
			out, shErr := cmd.Output()
			shRoot := strings.TrimSpace(string(out))
			shOK := shErr == nil && shRoot != ""
			if shOK != ok {
				t.Fatalf("shell ok=%v (%q, %v), Go ok=%v (%q)", shOK, shRoot, shErr, ok, root)
			}
			shOut := observe(t, filepath.Dir(filepath.Dir(orDot(shRoot))), shRoot, shOK)
			if goOut != shOut {
				t.Errorf("shell %+v, Go %+v", shOut, goOut)
			}
			if ok && !strings.HasSuffix(shRoot, filepath.Join(".rota", "tmp")) {
				t.Errorf("shell root %q is not <main>/.rota/tmp", shRoot)
			}
		})
	}
}

func orDot(p string) string {
	if p == "" {
		return "."
	}
	return p
}
