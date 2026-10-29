// Package rotastatetest holds the real-git lookups tests use to find the state
// directory of a repository they built.
package rotastatetest

import (
	"context"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/rotastate"
)

// CommonDir is the symlink-resolved git common dir of dir through real git.
// Production code holds a git runner and calls rotastate.CommonDirVia, so a
// fake git reaches the lookup.
func CommonDir(dir string) (string, error) {
	return rotastate.CommonDirVia(context.Background(), git.Exec, dir)
}
