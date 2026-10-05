// Package rotastate is the one place that knows where rota keeps per-checkout
// runtime state: it resolves the git common dir (symlink-safe, so every
// worktree of a repository and every spelling of its path agree) and owns the
// <git-common-dir>/rota/ layout. Packages ask it for a path and keep only the
// name of their own file.
package rotastate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/l4ci/rota/internal/git"
)

// dirName is the directory under the git common dir that holds rota's state.
const dirName = "rota"

// ErrNotRepo is returned when the directory is not inside a git repository.
var ErrNotRepo = errors.New("not a git repository")

// CommonDir is the symlink-resolved git common dir of dir.
func CommonDir(dir string) (string, error) {
	return CommonDirVia(context.Background(), git.Exec, dir)
}

// CommonDirVia is CommonDir through a git runner, for callers that fake git.
func CommonDirVia(ctx context.Context, run git.Runner, dir string) (string, error) {
	p, ok, err := git.CommonDirVia(ctx, run, dir)
	if err == nil && !ok {
		err = ErrNotRepo
	}
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir in %s: %w", dir, err)
	}
	return p, nil
}

// Dir is the state directory under a common dir.
func Dir(commonDir string) string { return filepath.Join(commonDir, dirName) }

// File is a state file named name under a common dir.
func File(commonDir, name string) string { return filepath.Join(Dir(commonDir), name) }

// SessionDir holds one state file per Claude Code session.
func SessionDir(commonDir string) string { return filepath.Join(Dir(commonDir), "session") }

// CodexDir holds the per-slot CODEX_HOME directories.
func CodexDir(commonDir string) string { return filepath.Join(Dir(commonDir), "codex") }

// MainCheckout is the main work tree of the repository a common dir belongs
// to: its parent when the common dir is a .git directory. ok is false for a
// bare repository or any other layout.
func MainCheckout(commonDir string) (string, bool) {
	if filepath.Base(commonDir) != ".git" {
		return "", false
	}
	return filepath.Dir(commonDir), true
}
