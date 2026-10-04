package cli

import (
	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/git"
)

// branchTarget is what a branch-reading verb (review scope, review brief,
// ship body) works on.
type branchTarget struct {
	Dir    string // the checkout git runs in: the --repo sub-repo, else the cwd
	Branch string // the named branch, else the current one
	Base   string // the resolved base branch
	// CorpusRoot holds the .rota/ whose BACKLOG.md and ARCHIVE.md item IDs are
	// looked up in: the umbrella resolved from the cwd before any --repo
	// (hv-resolve-umbrella), else the physical cwd, as the old helpers did.
	CorpusRoot string
}

// resolveBranch resolves a branch-reading verb's optional <branch> argument:
// exit 2 at an umbrella root without --repo, 3 when HEAD is detached and no
// branch is named, when no base branch resolves, or when the branch does
// not exist. Comparing the branch with the base is left to the verb, whose
// message differs.
func resolveBranch(c *Ctx, args []string) (branchTarget, error) {
	if len(args) > 1 {
		return branchTarget{}, Usage("unexpected argument %q", args[1])
	}
	cwd, err := os.Getwd()
	if err != nil {
		return branchTarget{}, err
	}
	t := branchTarget{CorpusRoot: cwd}
	if real, err := filepath.EvalSymlinks(cwd); err == nil {
		t.CorpusRoot = real
	}
	if root, err := git.FindRoot(cwd, registeredRels); err == nil {
		t.CorpusRoot = root
	}
	if t.Dir, err = gitDir(c); err != nil {
		return branchTarget{}, err
	}
	if t.Dir == "" {
		t.Dir = cwd
	}
	ctx := c.Context()
	r := git.Repo{Dir: t.Dir}
	if len(args) == 1 && args[0] != "" {
		t.Branch = args[0]
	} else {
		if t.Branch, err = r.CurrentBranch(ctx); err != nil {
			return branchTarget{}, gitErr(err)
		}
		if t.Branch == "" {
			return branchTarget{}, Resolution("no branch given and HEAD is not on a branch")
		}
	}
	base, ok, err := resolveBase(ctx, t.Dir)
	if err != nil {
		return branchTarget{}, err
	}
	if !ok {
		return branchTarget{}, Resolution("could not determine base branch (tried git.baseBranch, main, master, trunk, origin/HEAD)")
	}
	t.Base = base
	found, err := r.Verify(ctx, t.Branch+"^{commit}")
	if err != nil {
		return branchTarget{}, gitErr(err)
	}
	if !found {
		return branchTarget{}, Resolution("branch '%s' not found", t.Branch)
	}
	return t, nil
}
