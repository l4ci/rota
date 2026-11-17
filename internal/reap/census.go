package reap

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/git"
)

// Count is how many leftovers a class holds and their total size in bytes.
// Size is 0 for a class with no bytes of its own (a prunable checkout).
type Count struct {
	N    int
	Size int64
}

// Census is the read-only tally doctor reports of what a run left behind.
type Census struct {
	LeakedTemp Count // smoke, gate and review dirs in the system temp dir
	StaleTmp   Count // project temp root entries `rota reap --kind tmp` lists
	Prunable   Count // .worktrees/ checkouts git marks prunable
}

// CensusInput names where to count. Root "" skips the worktree class and an
// empty TempRoot or SystemTemp skips theirs.
type CensusInput struct {
	Root       string
	SystemTemp string
	TempRoot   string
	Git        git.Runner
	Now        time.Time
}

// leakedPrefixes are the system temp dirs a smoke run, a gate run and a
// review workspace leave behind.
var leakedPrefixes = []string{"rota-smoke.", "rota-gate-", ReviewWorkspacePrefix}

// TakeCensus counts each class by the rule reap lists it with.
func TakeCensus(ctx context.Context, in CensusInput) Census {
	var c Census
	c.LeakedTemp = leakedTemp(in.SystemTemp, in.Now.Add(-LeftoverAge))
	if in.TempRoot != "" {
		c.StaleTmp.N, c.StaleTmp.Size = StaleTmp(in.TempRoot, in.Now)
	}
	if in.Root != "" {
		c.Prunable.N = prunable(ctx, in.Git, in.Root)
	}
	return c
}

// leakedTemp counts the leakedPrefixes dirs in dir last modified before
// cutoff, and their total size.
func leakedTemp(dir string, cutoff time.Time) (c Count) {
	if dir == "" {
		return c
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return c
	}
	for _, e := range entries {
		if !e.IsDir() || !hasAnyPrefix(e.Name(), leakedPrefixes) {
			continue
		}
		if fi, err := e.Info(); err != nil || !fi.ModTime().Before(cutoff) {
			continue
		}
		c.N++
		filepath.WalkDir(filepath.Join(dir, e.Name()), func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if fi, err := d.Info(); err == nil {
					c.Size += fi.Size()
				}
			}
			return nil
		})
	}
	return c
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// prunable counts the .worktrees/ checkouts git marks prunable: the ones
// findWorktrees lists.
func prunable(ctx context.Context, run git.Runner, root string) int {
	res, err := run(ctx, root, "worktree", "list", "--porcelain")
	if err != nil || res.ExitCode != 0 {
		return 0
	}
	n := 0
	for _, w := range git.ParseWorktrees(res.Stdout) {
		if w.Prunable && ScratchCheckout(root, w.Path) {
			n++
		}
	}
	return n
}
