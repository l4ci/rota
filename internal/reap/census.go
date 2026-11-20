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
	LeakedTemp Count    // smoke, gate and review dirs in the system temp dir
	StaleTmp   Count    // project temp root entries reap lists and does not hold
	Prunable   Count    // .worktrees/ checkouts git marks prunable
	Warnings   []string // what reap warns while listing tmp, so doctor can show it
}

// CensusInput names where to count. Root "" skips the worktree class and an
// empty TempRoot or SystemTemp skips theirs.
type CensusInput struct {
	Root       string
	SystemTemp string
	// Tmp is reap's own input; the tmp class is the candidates `rota reap
	// --kind tmp` lists that are not held. Nil skips the class.
	Tmp *Input
	Git git.Runner
	Now time.Time
}

// leakedPrefixes are the system temp dirs a smoke run, a gate run and a
// review workspace leave behind.
var leakedPrefixes = []string{"rota-smoke.", "rota-gate-", ReviewWorkspacePrefix}

// TakeCensus counts each class by the rule reap lists it with.
func TakeCensus(ctx context.Context, in CensusInput) Census {
	var c Census
	c.LeakedTemp = leakedTemp(in.SystemTemp, in.Now.Add(-LeftoverAge))
	if in.Tmp != nil && in.Tmp.TempRoot != "" {
		c.StaleTmp, c.Warnings = reclaimableTmp(ctx, *in.Tmp)
	}
	if in.Root != "" {
		c.Prunable.N = prunable(ctx, in.Git, in.Root)
	}
	return c
}

// reclaimableTmp counts the tmp candidates reap lists that its held rules
// leave free to delete: what `rota reap --kind tmp --apply` would remove.
// Anything reap warns about (or a Find that fails) comes back as a warning.
func reclaimableTmp(ctx context.Context, in Input) (c Count, warnings []string) {
	res, err := Find(ctx, in, []string{KindTmp})
	if err != nil {
		return c, []string{"temp root not checked: " + err.Error()}
	}
	for _, cd := range res.Candidates {
		if cd.Held != "" {
			continue
		}
		c.N++
		size, _, _, _ := entryStat(cd.Path)
		c.Size += size
	}
	return c, res.Warnings
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
