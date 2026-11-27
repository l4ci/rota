package reap

import (
	"context"
	"strings"

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
	LeakedTemp    Count    // systmp candidates reap lists and does not hold
	StaleTmp      Count    // project temp root entries reap lists and does not hold
	Prunable      Count    // .worktrees/ checkouts git marks prunable
	Warnings      []string // what reap warns while listing tmp, so doctor can show it
	CanReapSysTmp bool     // the systmp count came from a runnable project reap input
}

// CensusInput names where to count. Root "" skips the worktree class and an
// empty TempRoot or SystemTemp skips theirs.
type CensusInput struct {
	Root       string
	SystemTemp string
	// Reap is reap's own input, the one `rota reap` lists with. The tmp class
	// is the candidates `rota reap --kind tmp` lists that are not held, the
	// worktree class the prunable unheld ones of `--kind worktree`. Nil skips
	// both.
	Reap *Input
	// ProcessUses is the process table reader the systmp class proves no live
	// process uses a dir with. When set it is authoritative, Tmp's own is
	// ignored for that class; when nil, Tmp's is used (nil there holds every dir).
	ProcessUses func() ([]ProcUse, error)
	Git         git.Runner
}

// TakeCensus counts each class by the rule reap lists it with.
func TakeCensus(ctx context.Context, in CensusInput) Census {
	var c Census
	if in.SystemTemp != "" {
		var warn []string
		c.LeakedTemp, c.CanReapSysTmp, warn = reclaimableSysTmp(ctx, in)
		c.Warnings = append(c.Warnings, warn...)
	}
	if in.Reap != nil && in.Reap.TempRoot != "" {
		var warn []string
		c.StaleTmp, warn = reclaimableTmp(ctx, *in.Reap)
		c.Warnings = append(c.Warnings, warn...)
	}
	if in.Root != "" && in.Reap != nil {
		var warn []string
		c.Prunable, warn = prunableWorktrees(ctx, *in.Reap)
		c.Warnings = append(c.Warnings, warn...)
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
	return unheld(res.Candidates), res.Warnings
}

// unheld counts the candidates reap's held rules leave free to delete.
func unheld(cands []Candidate) (c Count) {
	for _, cd := range cands {
		if cd.Held != "" {
			continue
		}
		c.N++
		size, _, _, _ := entryStat(cd.Path)
		c.Size += size
	}
	return c
}

// reclaimableSysTmp counts the systmp candidates reap lists that its held
// rules leave free to delete: what `rota reap --kind systmp --apply` would
// remove. With a project it runs through Find, so a git worktree or host agent
// under a dir holds it; without one the process table alone proves it free.
func reclaimableSysTmp(ctx context.Context, in CensusInput) (Count, bool, []string) {
	ri := Input{Root: in.Root, Git: in.Git, ProcessUses: in.ProcessUses}
	if in.Reap != nil {
		ri = *in.Reap
	}
	ri.SystemTemp = in.SystemTemp
	if in.ProcessUses != nil {
		ri.ProcessUses = in.ProcessUses
	}
	bare := func() (Count, bool, []string) {
		s := &state{in: ri, live: newOwnership(nil, "", "", nil)}
		cands, _ := findSysTmp(ctx, s)
		return unheld(cands), false, s.warnings
	}
	if in.Root == "" || ri.Git == nil {
		return bare()
	}
	res, err := Find(ctx, ri, []string{KindSysTmp})
	if err != nil {
		// A project dir that is no git repo has no checkouts for a dir to hold;
		// the process table still judges it. Any other git failure stays closed.
		if strings.Contains(err.Error(), "not a git repository") {
			return bare()
		}
		return Count{}, false, []string{"system temp dir not checked: " + err.Error()}
	}
	return unheld(res.Candidates), in.Reap != nil, res.Warnings
}

// prunableWorktrees counts the worktree candidates reap lists that git marks
// prunable and its held rules leave free: what `rota reap --kind worktree
// --apply` would remove of them. With the host unreadable reap lists none.
func prunableWorktrees(ctx context.Context, in Input) (c Count, warnings []string) {
	in.PrunableOnly = true
	res, err := Find(ctx, in, []string{KindWorktree})
	if err != nil {
		// A project dir that is no git repo has no checkouts to count.
		if strings.Contains(err.Error(), "not a git repository") {
			return c, nil
		}
		return c, []string{"worktrees not checked: " + err.Error()}
	}
	return unheld(res.Candidates), nil
}
