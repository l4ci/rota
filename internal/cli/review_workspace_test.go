package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/proc"
)

func TestReviewWorkspacePinnedAndCleanup(t *testing.T) {
	repo := newRepo(t, t.TempDir(), "repo", "main")
	write(t, filepath.Join(repo, "tracked.txt"), "pinned\n")
	gitT(t, repo, "add", "tracked.txt")
	gitT(t, repo, "commit", "-qm", "pinned")
	sha := gitT(t, repo, "rev-parse", "HEAD")
	write(t, filepath.Join(repo, "tracked.txt"), "live dirty\n")
	before := gitT(t, repo, "status", "--porcelain")
	o := trRun(t, repo, "", "review", "prepare", sha, "--json")
	if o.code != 0 {
		t.Fatalf("prepare must return a workspace: rc=%d %s %s", o.code, o.stdout, o.stderr)
	}
	data := envelope(t, o.stdout)["data"].(map[string]any)
	path := data["path"].(string)
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(path)) })
	if data["head"] != sha {
		t.Fatalf("head=%v want %s", data["head"], sha)
	}
	if got, err := os.ReadFile(filepath.Join(path, "tracked.txt")); err != nil || string(got) != "pinned\n" {
		t.Fatalf("workspace content=%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(path, ".review")); err != nil {
		t.Fatal(err)
	}
	o = trRun(t, repo, "", "review", "cleanup", path, "--json")
	if o.code != 0 {
		t.Fatalf("cleanup: %s %s", o.stdout, o.stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("workspace survived: %v", err)
	}
	if got := gitT(t, repo, "status", "--porcelain"); got != before {
		t.Fatalf("live status changed: %s -> %s", before, got)
	}
}

// Snapshot includes staged and unstaged bytes, HEAD, the index and registry.
func reviewLiveSnapshot(t *testing.T, repo string) []string {
	t.Helper()
	status := gitT(t, repo, "status", "--porcelain")
	idx, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(repo, "tracked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return []string{status, gitT(t, repo, "rev-parse", "HEAD"), string(idx), gitT(t, repo, "worktree", "list", "--porcelain"), string(content)}
}

func TestReviewWorkspaceFailuresPreserveLiveRepo(t *testing.T) {
	for _, mode := range []string{"missing", "archive", "extract", "setup", "cleanup", "extract-cleanup"} {
		t.Run(mode, func(t *testing.T) {
			temp := t.TempDir()
			t.Setenv("TMPDIR", temp)
			repo := newRepo(t, t.TempDir(), "repo", "main")
			write(t, filepath.Join(repo, "tracked.txt"), "committed")
			if mode == "setup" {
				write(t, filepath.Join(repo, ".review", "existing"), "reserved")
			}
			gitT(t, repo, "add", "tracked.txt")
			if mode == "setup" {
				gitT(t, repo, "add", ".review")
			}
			gitT(t, repo, "commit", "-qm", "content")
			write(t, filepath.Join(repo, "tracked.txt"), "staged")
			gitT(t, repo, "add", "tracked.txt")
			write(t, filepath.Join(repo, "tracked.txt"), "unstaged")
			write(t, filepath.Join(repo, "untracked"), "keep")
			before := reviewLiveSnapshot(t, repo)
			deps := testDeps()
			ref := "HEAD"
			if mode == "missing" {
				ref = "does-not-exist"
			}
			if mode == "archive" {
				realGit := deps.Git
				deps.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
					if args[0] == "archive" {
						return git.Result{ExitCode: 7, Stderr: "archive failed"}, nil
					}
					return realGit(ctx, dir, args...)
				}
			}
			extracted := false
			if strings.HasPrefix(mode, "extract") {
				deps.Proc = func(ctx context.Context, cmd proc.Cmd) (proc.Result, error) {
					if cmd.Name != "tar" {
						t.Fatalf("unexpected process: %+v", cmd)
					}
					extracted = true
					write(t, filepath.Join(cmd.Args[3], "partial"), "partial extraction")
					return proc.Result{ExitCode: 2, Stderr: "extraction failed"}, nil
				}
			}
			if mode == "extract-cleanup" {
				deps.ReviewRemoveAll = func(string) error { return errors.New("preparation cleanup denied") }
			}
			o := trRunWith(t, deps, repo, "", "review", "prepare", ref, "--json")
			if mode == "cleanup" {
				if o.code != 0 {
					t.Fatalf("prepare: %s", o.stdout)
				}
				path := envelope(t, o.stdout)["data"].(map[string]any)["path"].(string)
				deps.ReviewRemoveAll = func(string) error { return errors.New("removal denied") }
				o = trRunWith(t, deps, repo, "", "review", "cleanup", path, "--json")
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("failed cleanup removed workspace: %v", err)
				}
				if !strings.Contains(o.stdout, "removal denied") {
					t.Fatalf("missing cleanup failure: %s", o.stdout)
				}
			}
			if o.code == 0 {
				t.Fatalf("%s failure returned launchable success: %s", mode, o.stdout)
			}
			if strings.HasPrefix(mode, "extract") && !extracted {
				t.Fatal("extraction failure never exercised")
			}
			if mode == "extract-cleanup" {
				if !strings.Contains(o.stdout, "preparation cleanup denied") || !strings.Contains(o.stdout, "cleanup failed for") {
					t.Fatalf("cleanup failure hidden: %s", o.stdout)
				}
			} else if mode != "cleanup" {
				entries, err := os.ReadDir(temp)
				if err != nil || len(entries) != 0 {
					t.Fatalf("failed preparation leaked: %v %v", entries, err)
				}
			}
			if got := reviewLiveSnapshot(t, repo); !reflect.DeepEqual(got, before) {
				t.Fatalf("live repository changed: before %q after %q", before, got)
			}
		})
	}
}

func TestReviewWorkspaceCleanupScope(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	repo := newRepo(t, t.TempDir(), "repo", "main")
	write(t, filepath.Join(repo, "keep"), "live")
	o := trRun(t, repo, "", "review", "cleanup", repo, "--json")
	if o.code != 4 {
		t.Fatalf("cleanup accepted live repository: %s", o.stdout)
	}
	o = trRun(t, repo, "", "review", "prepare", "HEAD", "--json")
	if o.code != 0 {
		t.Fatalf("prepare: %s", o.stdout)
	}
	path := envelope(t, o.stdout)["data"].(map[string]any)["path"].(string)
	// An agent may replace the tree with a symlink. Cleanup removes the link,
	// not its target, and also leaves a separately prepared workspace alone.
	other := trRun(t, repo, "", "review", "prepare", "HEAD", "--json")
	if other.code != 0 {
		t.Fatalf("second prepare: %s", other.stdout)
	}
	otherPath := envelope(t, other.stdout)["data"].(map[string]any)["path"].(string)
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, path); err != nil {
		t.Fatal(err)
	}
	o = trRun(t, repo, "", "review", "cleanup", path, "--json")
	if o.code != 0 {
		t.Fatalf("cleanup: %s", o.stdout)
	}
	if got, err := os.ReadFile(filepath.Join(repo, "keep")); err != nil || string(got) != "live" {
		t.Fatalf("live target changed: %q %v", got, err)
	}
	if _, err := os.Stat(otherPath); err != nil {
		t.Fatalf("other workspace removed: %v", err)
	}
}

// A rota project keeps review workspaces under its own .rota/tmp, resolved
// through the git common dir so a linked worktree lands in the main checkout.
func TestReviewWorkspaceUnderProjectTemp(t *testing.T) {
	repo := newRepo(t, t.TempDir(), "repo", "main")
	write(t, filepath.Join(repo, "tracked.txt"), "x\n")
	gitT(t, repo, "add", "tracked.txt")
	gitT(t, repo, "commit", "-qm", "x")
	sha := gitT(t, repo, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(repo, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	gitT(t, repo, "worktree", "add", "-q", "--detach", wt, sha)
	canon, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canon, ".rota", "tmp")
	for _, cwd := range []string{repo, wt} {
		o := trRun(t, cwd, "", "review", "prepare", sha, "--json")
		if o.code != 0 {
			t.Fatalf("prepare from %s: rc=%d %s %s", cwd, o.code, o.stdout, o.stderr)
		}
		path := envelope(t, o.stdout)["data"].(map[string]any)["path"].(string)
		if got := filepath.Dir(filepath.Dir(path)); got != want {
			t.Fatalf("workspace parent = %s, want %s", got, want)
		}
		o = trRun(t, cwd, "", "review", "cleanup", path, "--json")
		if o.code != 0 {
			t.Fatalf("cleanup from %s: %s %s", cwd, o.stdout, o.stderr)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("workspace survived: %v", err)
		}
	}
	// A look-alike workspace elsewhere in the project is still refused.
	fake := filepath.Join(canon, ".rota", "rota-review-x", "tree")
	if err := os.MkdirAll(fake, 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(filepath.Dir(fake), reviewWorkspaceMarker), `{"path":"`+fake+`","head":"h"}`)
	if o := trRun(t, repo, "", "review", "cleanup", fake, "--json"); o.code == 0 {
		t.Fatalf("cleanup outside the temp roots must be refused: %s", o.stdout)
	}
}
