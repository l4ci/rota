package ship

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/status"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// Checkout is where the caller runs the merge. Cwd is its working directory,
// which ClearWorktree never removes; Dir is the checkout g runs in; Repo is the
// sub-repo name status entries are matched against ("" resolves it from Dir).
type Checkout struct {
	Cwd, Dir, Repo string
	// Owners is where ownership is read from; nil reads the project's
	// worker registry and status files from disk.
	Owners Owners
}

// Cycle is a status entry that registers a worktree as /rota-work cycle work.
type Cycle struct{ Branch, Repo, Worktree string }

// Owners is what ClearWorktree knows about who owns a worktree. primary is the
// first entry of the worktree listing.
type Owners interface {
	// Roots are the project roots whose state may register the worktree.
	Roots(primary string, at Checkout) []string
	// Slots are the round slots recorded under root.
	Slots(root string) ([]worker.Holder, error)
	// Cycles are the status entries recorded under root.
	Cycles(root string) []Cycle
	// Repo is the sub-repo name of the checkout in dir ("" when none).
	Repo(dir string) string
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
		return &Refusal{By: blockedby.Worktree, Msg: "worktree contains the current directory: " + wt, Hint: "run ship merge from outside this worktree"}
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
		return &Refusal{By: blockedby.Worktree, Msg: "worktree has uncommitted or ignored files: " + wt, Hint: "preserve the files before retrying ship merge"}
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
	primary := ""
	if wts := git.ParseWorktrees(listing); len(wts) > 0 {
		primary = wts[0].Path
	}
	own := at.Owners
	if own == nil {
		own = diskOwners{}
	}
	roots := own.Roots(primary, at)
	for _, root := range roots {
		holders, err := own.Slots(root)
		if err != nil {
			return &GitError{Msg: fmt.Sprintf("cannot read round slots: %v", err)}
		}
		if name := worker.HeldBy(root, holders, wt); name != "" {
			return &Refusal{By: blockedby.Worktree, Msg: "worktree belongs to round slot " + name + ": " + wt, Hint: "leave slot cleanup to the round orchestrator"}
		}
	}
	repo := at.Repo
	if repo == "" {
		repo = own.Repo(at.Dir)
	}
	for _, root := range roots {
		for _, c := range own.Cycles(root) {
			if c.Branch != branch || c.Repo != repo || c.Worktree == "" {
				continue
			}
			path := c.Worktree
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			if worker.PathContains(path, wt) && worker.PathContains(wt, path) {
				return nil
			}
		}
	}
	return &Refusal{By: blockedby.Worktree, Msg: "worktree is not registered as cycle work: " + wt, Hint: "preserve this worktree; use the owning workflow to manage it"}
}

// diskOwners reads ownership from the project's .rota/ state.
type diskOwners struct{}

func (diskOwners) Roots(primary string, at Checkout) []string {
	return []string{projectRoot(primary), primary, projectRoot(at.Dir)}
}

func (diskOwners) Slots(root string) ([]worker.Holder, error) {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return nil, err
	}
	var out []worker.Holder
	for _, s := range reg.Slots() {
		out = append(out, worker.Holder{Name: s.Name(), Worktree: s.Worktree()})
	}
	return out, nil
}

func (diskOwners) Cycles(root string) []Cycle {
	var out []Cycle
	for _, e := range status.Entries(root) {
		out = append(out, Cycle{Branch: e.Branch, Repo: e.Repo, Worktree: e.Worktree})
	}
	return out
}

func (diskOwners) Repo(dir string) string {
	if r, err := repos.Which(dir); err == nil {
		return r.Name
	}
	return ""
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
	if root, err := rotatree.FindRoot(dir, registered); err == nil {
		return root
	}
	return dir
}

// linkedWorktree is the path of the linked (not the first) worktree in a
// porcelain listing that has branch checked out, "" if none.
func linkedWorktree(porcelain, branch string) string {
	wts := git.ParseWorktrees(porcelain)
	for i := 1; i < len(wts); i++ {
		if wts[i].Branch == branch {
			return wts[i].Path
		}
	}
	return ""
}
