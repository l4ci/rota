package extknowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// State is what a clone directory looks like on disk. A field is only probed
// once the ones before it hold, so a false field means "not found" or "not
// checked".
type State struct {
	Exists    bool // the path exists and is a directory
	IsGit     bool // it is a git work tree
	HasRemote bool // the clone has a remote to fetch from and push to
	HasPolicy bool // rota-knowledge.json is at its root
}

// State probes the clone without changing it.
func (c Clone) State(ctx context.Context) State {
	var s State
	if fi, err := os.Stat(c.Dir); err != nil || !fi.IsDir() {
		return s
	}
	s.Exists = true
	if out, err := c.out(ctx, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return s
	}
	s.IsGit = true
	s.HasRemote = c.hasRemote(ctx)
	_, err := os.Stat(filepath.Join(c.Dir, PolicyFile))
	s.HasPolicy = err == nil
	return s
}
