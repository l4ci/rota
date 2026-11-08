package cli

import (
	"context"
	"errors"

	"github.com/l4ci/rota/internal/repos"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/rotatree"
)

// findRoot is the one project-root lookup in cli: the nearest directory at or
// above dir that holds .rota/. With masking on, a stray .rota/ inside a
// sub-repo that a higher .rota/ registers fails with rotatree.ErrMasked;
// with it off the nearest .rota/ wins. A missing root is rotatree.ErrNoRoot
// either way, so callers tell masked and missing apart with errors.Is.
func findRoot(dir string, masking bool) (string, error) {
	if masking {
		return rotatree.FindRoot(dir, repos.Rels)
	}
	if root, ok := rotatree.Find(dir); ok {
		return root, nil
	}
	return "", rotatree.ErrNoRoot
}

// rootOrDir is the project whose .rota/ holds the config and backlog of the
// checkout in dir (masking on), dir itself when none resolves.
func rootOrDir(dir string) string {
	if root, err := findRoot(dir, true); err == nil {
		return root
	}
	return dir
}

// rootError is the user-facing error for a failed findRoot: masked and
// missing read differently.
func rootError(err error) *Error {
	if errors.Is(err, rotatree.ErrMasked) {
		return Resolution("%v", err).WithHint("remove the stray .rota/ inside the sub-repo")
	}
	return Resolution("no .rota/ directory here or in any parent").WithHint("run: rota init")
}

// CommonDir is the git common dir of dir through the injected git runner, the
// parent of the rota state directory. The error is rotastate's: it wraps
// rotastate.ErrNotRepo outside a repository, and each caller keeps its own
// exit code for it.
func (c *Ctx) CommonDir(dir string) (string, error) {
	return c.CommonDirCtx(c.Context(), dir)
}

// CommonDirCtx is CommonDir under ctx, for a loop that runs on its own
// cancellable context rather than the verb's.
func (c *Ctx) CommonDirCtx(ctx context.Context, dir string) (string, error) {
	return rotastate.CommonDirVia(ctx, c.deps().Git, dir)
}

// CommonDirOK is CommonDir for callers that skip the state when dir is not in
// a repository or git fails.
func (c *Ctx) CommonDirOK(dir string) (string, bool) {
	cd, err := c.CommonDir(dir)
	return cd, err == nil
}

// MainCheckout is the main checkout of the repository that holds root (the
// parent of the git common dir). ok is false outside a repository or when the
// common dir is not a .git directory.
func (c *Ctx) MainCheckout(root string) (string, bool) {
	cd, ok := c.CommonDirOK(root)
	if !ok {
		return "", false
	}
	return c.MainCheckoutOf(cd)
}

// MainCheckoutOf is MainCheckout for a caller that already holds the common
// dir, so it makes no second git lookup.
func (c *Ctx) MainCheckoutOf(commonDir string) (string, bool) {
	return rotastate.MainCheckout(commonDir)
}
