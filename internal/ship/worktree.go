package ship

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/status"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// Checkout is where the caller runs the merge. Cwd is its working directory,
// which ClearWorktree never removes; Dir is the checkout g runs in; Repo is the
// sub-repo name status entries are matched against ("" resolves it from Dir).
type Checkout struct {
	Cwd, Dir, Repo string
}

// ClearWorktree removes a clean cycle worktree before a local merge. It owns
// the deletion rule: round-slot worktrees and worktrees not registered in
// status as cycle work are refused. Never remove the caller's cwd, even when
// it is a subdirectory or symlink.
func ClearWorktree(g Git, branch string, at Checkout) error {
	res, err := g.Run("worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return &GitError{Msg: "git worktree list: " + strutil.FirstLine(res.Stderr)}
	}
	wt := linkedWorktree(res.Stdout, branch)
	if wt == "" {
		return nil
	}
	if worker.PathContains(wt, at.Cwd) {
		return &Refusal{By: "worktree", Msg: "worktree contains the current directory: " + wt, Hint: "run ship merge from outside this worktree"}
	}
	if err := checkOwnership(res.Stdout, at, branch, wt); err != nil {
		return err
	}
	// Include ignored files: git worktree remove otherwise silently deletes
	// ignored scratch files. Never use --force, including after this check.
	dirty, err := g.Run("-C", wt, "status", "--porcelain", "--untracked-files=all", "--ignored")
	if err != nil {
		return err
	}
	if dirty.ExitCode != 0 {
		return &GitError{Msg: "git status " + wt + ": " + strutil.FirstLine(dirty.Stderr)}
	}
	if strings.TrimSpace(dirty.Stdout) != "" {
		return &Refusal{By: "worktree", Msg: "worktree has uncommitted or ignored files: " + wt, Hint: "preserve the files before retrying ship merge"}
	}
	rm, err := g.Run("worktree", "remove", wt)
	if err != nil {
		return err
	}
	if rm.ExitCode != 0 {
		return &GitError{Msg: "git worktree remove " + wt + ": " + strutil.FirstLine(rm.Stderr)}
	}
	return nil
}

// checkOwnership recognizes /rota-work's status registration. A branch match
// or a familiar directory name alone never authorizes deletion. listing is the
// porcelain worktree list, whose first entry is the primary checkout.
func checkOwnership(listing string, at Checkout, branch, wt string) error {
	primary := strings.TrimPrefix(strings.SplitN(listing, "\n", 2)[0], "worktree ")
	roots := []string{projectRoot(primary), primary, projectRoot(at.Dir)}
	resolve := func(root, path string) string {
		if filepath.IsAbs(path) {
			return path
		}
		return filepath.Join(root, path)
	}
	for _, root := range roots {
		reg, err := worker.LoadRegistry(root)
		if err != nil {
			return &GitError{Msg: fmt.Sprintf("cannot read round slots: %v", err)}
		}
		for _, slot := range reg.Slots() {
			if path := slot.Worktree(); path != "" && worker.PathContains(resolve(root, path), wt) {
				return &Refusal{By: "worktree", Msg: "worktree belongs to round slot " + slot.Name() + ": " + wt, Hint: "leave slot cleanup to the round orchestrator"}
			}
		}
	}
	repo := at.Repo
	if repo == "" {
		if r, err := repos.Which(at.Dir); err == nil {
			repo = r.Name
		}
	}
	for _, root := range roots {
		for _, entry := range status.Entries(root) {
			if entry.Branch != branch || entry.Repo != repo || entry.Worktree == "" {
				continue
			}
			path := resolve(root, entry.Worktree)
			if worker.PathContains(path, wt) && worker.PathContains(wt, path) {
				return nil
			}
		}
	}
	return &Refusal{By: "worktree", Msg: "worktree is not registered as cycle work: " + wt, Hint: "preserve this worktree; use the owning workflow to manage it"}
}

// projectRoot is the project whose .rota/ holds the state of the checkout in
// dir, dir itself when none resolves.
func projectRoot(dir string) string {
	registered := func(root string) []string {
		var out []string
		for _, r := range repos.Load(root) {
			out = append(out, r.Rel)
		}
		return out
	}
	if root, err := git.FindRoot(dir, registered); err == nil {
		return root
	}
	return dir
}

// linkedWorktree is the path of the linked (not the first) worktree in a
// porcelain listing that has branch checked out, "" if none.
func linkedWorktree(porcelain, branch string) string {
	wt, count := "", 0
	for _, l := range strings.Split(porcelain, "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			wt = p
			count++
		} else if l == "branch refs/heads/"+branch && count > 1 {
			return wt
		}
	}
	return ""
}
