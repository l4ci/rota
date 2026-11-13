package cli

import (
	"path/filepath"

	"github.com/l4ci/rota/internal/rotastate"
)

// tempRootFor is where rota makes its own temp workspaces for work in dir: the
// project temp root (<main checkout>/.rota/tmp, #2499), else the system temp
// dir outside a rota project. Both come back symlink-resolved, so cleanup can
// anchor on either as an exact parent.
func (c *Ctx) tempRootFor(dir string) (string, error) {
	root, ok, err := rotastate.TempRootVia(c.Context(), c.deps().Git, dir)
	if err != nil {
		return "", err
	}
	if ok {
		return root, nil
	}
	return systemTempDir(c.deps().TempDir())
}

// systemTempDir is the OS temp dir, absolute and symlink-resolved.
func systemTempDir(dir string) (string, error) {
	temp, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(temp)
}
