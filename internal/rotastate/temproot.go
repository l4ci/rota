package rotastate

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotatree"
)

// selfIgnore is the temp root's own .gitignore: it ignores everything under
// the root, itself included, so a project whose .gitignore predates the root
// still keeps it out of git status, classify and footprint overlap.
const selfIgnore = "*\n"

// TempRoot is the project temp root of the repository a common dir belongs
// to: <main checkout>/.rota/tmp, so a worker's runs and the orchestrator's land
// in one place. ok is false when there is no main checkout (a bare repository)
// or it holds no .rota/ (not a rota project); callers then use the system temp
// dir. It creates nothing.
func TempRoot(commonDir string) (string, bool) {
	main, ok := MainCheckout(commonDir)
	if !ok || !rotatree.Exists(main) {
		return "", false
	}
	return rotatree.File(main, rotatree.TmpDir), true
}

// TempRootVia is TempRoot for the repository that holds dir, created on first
// use (0700, self-ignoring) and symlink-resolved, so a caller can anchor on it
// as an exact parent. ok is false with a nil error outside a repository or
// rota project; err is a failure to create the root.
func TempRootVia(ctx context.Context, run git.Runner, dir string) (string, bool, error) {
	cd, err := CommonDirVia(ctx, run, dir)
	if errors.Is(err, ErrNotRepo) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	root, ok := TempRoot(cd)
	if !ok {
		return "", false, nil
	}
	if err := EnsureTempRoot(root); err != nil {
		return "", false, err
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false, err
	}
	return real, true, nil
}

// EnsureTempRoot creates root (0700) and its self-ignoring .gitignore when
// either is missing. An existing .gitignore is left as it is.
func EnsureTempRoot(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	ignore := filepath.Join(root, ".gitignore")
	if _, err := os.Lstat(ignore); err == nil || !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(ignore, []byte(selfIgnore), 0o600)
}
