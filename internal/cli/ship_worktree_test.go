package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/status"
)

// Use a throwaway clone and a local origin: this regression must never run
// destructive ship code against the developer's checkout or round slots.
func TestShipPRPreservesSlotWorktree(t *testing.T) {
	shipDeterministic(t)
	for _, where := range []string{"inside", "outside"} {
		for _, dirty := range []bool{false, true} {
			t.Run(where+map[bool]string{false: "/clean", true: "/dirty"}[dirty], func(t *testing.T) {
				seed := shipFixture(t, "")
				work := filepath.Join(t.TempDir(), "clone")
				gitT(t, seed, "clone", "-q", "--no-local", seed, work)
				branch := "nia/366-ship-worktree"
				shipBranchOf(t, work, branch, [3]string{"feature.txt", "feat: work", ""})
				wt := filepath.Join(work, ".worktrees", "nia")
				gitT(t, work, "worktree", "add", "-q", wt, branch)
				write(t, filepath.Join(work, ".rota", "workers.json"), `{"slots":[{"name":"nia","worktree":".worktrees/nia"}]}`)
				if dirty {
					write(t, filepath.Join(wt, "feature.txt"), "uncommitted\n")
					write(t, filepath.Join(wt, "untracked.txt"), "untracked\n")
				}
				dir := work
				if where == "inside" {
					dir = filepath.Join(wt, "nested")
					if err := os.Mkdir(dir, 0755); err != nil {
						t.Fatal(err)
					}
				}
				before := gitT(t, work, "worktree", "list", "--porcelain")
				o := trRunWith(t, useForge(t, shipPRForge("https://example.test/pull/366")), dir, "body", "ship", "pr", branch, "--title", "Fix", "--body-file", "-")
				if o.code != 0 {
					t.Errorf("ship pr: exit %d: %s", o.code, o.stderr)
				}
				if _, err := os.Stat(filepath.Join(wt, "feature.txt")); err != nil {
					t.Fatalf("slot worktree was removed: %v", err)
				}
				if got := gitT(t, work, "worktree", "list", "--porcelain"); got != before {
					t.Errorf("worktrees changed:\n%s", got)
				}
				if _, err := os.Stat(dir); err != nil {
					t.Errorf("cwd removed: %v", err)
				}
				if dirty {
					for file, want := range map[string]string{"feature.txt": "uncommitted\n", "untracked.txt": "untracked\n"} {
						got, err := os.ReadFile(filepath.Join(wt, file))
						if err != nil || string(got) != want {
							t.Errorf("%s lost: %q, %v", file, got, err)
						}
					}
				}
				if !strings.Contains(o.stdout, "/pull/366") {
					t.Errorf("PR not opened: %+v", o)
				}
			})
		}
	}
}

func TestShipMergeProtectsWorktrees(t *testing.T) {
	shipDeterministic(t)
	for _, kind := range []string{"unregistered", "slot", "cwd", "cwd-symlink", "tracked", "staged", "untracked", "ignored", "owned"} {
		t.Run(kind, func(t *testing.T) {
			seed := shipFixture(t, "")
			work := filepath.Join(t.TempDir(), "clone")
			gitT(t, seed, "clone", "-q", "--no-local", seed, work)
			branch := "nia/366-ship-worktree"
			shipBranchOf(t, work, branch, [3]string{"feature.txt", "feat: work", ""})
			wt := filepath.Join(work, ".claude", "worktrees", branch)
			gitT(t, work, "worktree", "add", "-q", wt, branch)
			if kind != "unregistered" {
				rel, _ := filepath.Rel(work, wt)
				if _, err := status.Add(work, branch, "", nil, rel, false); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "slot" {
				// A slot can have a nonstandard path and a stale cycle record.
				write(t, filepath.Join(work, ".rota", "workers.json"), `{"slots":[{"name":"nia","worktree":".claude/worktrees/nia/366-ship-worktree"}]}`)
			}
			dir := work
			if strings.HasPrefix(kind, "cwd") {
				dir = filepath.Join(wt, "nested")
				if err := os.Mkdir(dir, 0755); err != nil {
					t.Fatal(err)
				}
				if kind == "cwd-symlink" {
					alias := filepath.Join(t.TempDir(), "alias")
					if err := os.Symlink(dir, alias); err != nil {
						t.Fatal(err)
					}
					dir = alias
				}
			}
			switch kind {
			case "tracked", "staged":
				write(t, filepath.Join(wt, "feature.txt"), "uncommitted\n")
				if kind == "staged" {
					gitT(t, wt, "add", "feature.txt")
				}
			case "untracked", "ignored":
				write(t, filepath.Join(wt, "scratch.txt"), "uncommitted\n")
				if kind == "ignored" {
					write(t, filepath.Join(work, ".git", "info", "exclude"), "scratch.txt\n")
				}
			}
			head := gitT(t, work, "rev-parse", "main")
			before := gitT(t, wt, "status", "--porcelain", "--untracked-files=all", "--ignored")
			o := trRun(t, dir, "merge: work", "ship", "merge", branch, "--body-file", "-")
			if kind == "owned" {
				if o.code != 0 {
					t.Fatalf("owned cleanup: %+v", o)
				}
				if _, err := os.Stat(wt); !os.IsNotExist(err) {
					t.Errorf("owned worktree left: %v", err)
				}
				return
			}
			if o.code != ExitRefused {
				t.Errorf("unsafe cleanup should refuse: %+v", o)
			}
			if _, err := os.Stat(filepath.Join(wt, "feature.txt")); err != nil {
				t.Fatalf("worktree removed: %v", err)
			}
			if got := gitT(t, wt, "status", "--porcelain", "--untracked-files=all", "--ignored"); got != before {
				t.Errorf("worktree changed: %s", got)
			}
			if got := gitT(t, work, "rev-parse", "main"); got != head {
				t.Errorf("refused merge changed main")
			}
		})
	}
}
