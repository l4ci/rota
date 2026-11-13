package cli

import (
	"path/filepath"

	"github.com/l4ci/rota/internal/rotastate"
)

// tempRootFor is where rota makes its own temp workspaces for work in dir: the
// project temp root (<main checkout>/.rota/tmp, #2499; project true), else the
// system temp dir outside a rota project. Both come back symlink-resolved, so
// cleanup can anchor on either as an exact parent. A dir inside a project temp
// root (a prepared review tree, fenced from git) resolves to that root.
func (c *Ctx) tempRootFor(dir string) (root string, project bool, err error) {
	if root, ok := rotastate.EnclosingTempRoot(dir); ok {
		return root, true, nil
	}
	root, project, err = rotastate.TempRootVia(c.Context(), c.deps().Git, dir)
	if err != nil || project {
		return root, project, err
	}
	root, err = systemTempDir(c.deps().TempDir())
	return root, false, err
}

// systemTempDir is the OS temp dir, absolute and symlink-resolved.
func systemTempDir(dir string) (string, error) {
	temp, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(temp)
}
