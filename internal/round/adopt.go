package round

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Blocked reasons of an adoption (exit 4, `blockedBy`).
const (
	// BlockRegistered: a slot already holds the branch.
	BlockRegistered = "registered"
	// BlockHeld: a slot already holds the issue.
	BlockHeld = "held"
	// BlockExternal: a destructive verb was pointed at an adopted slot, whose
	// worktree and branch rota did not create.
	BlockExternal = worker.BlockExternal
)

// AdoptOpts are the flags of `rota worker adopt`.
type AdoptOpts struct {
	// Ref is a local branch name or a worktree path of this repo.
	Ref string
	// Issue is the item the work resolves, in the backend's spelling.
	Issue string
	// Name is the slot name; "" picks the next free ext-<n>.
	Name string
	// PR is the work's PR URL when it already has one.
	PR            string
	AcceptOverlap bool
	// Shared is round.sharedPaths, left out of the overlap footprints.
	Shared []string
}

// Adopted is the slot Adopt registered.
type Adopted struct {
	Slot, Branch, Worktree, Issue, PR string
	Overlaps                          []Overlap
}

// Adopt registers work another tool started as a hostless slot. It runs the
// overlap check `round assign` runs and refuses a blocking clash unless
// o.AcceptOverlap. A path outside this repo's worktrees or an unknown branch is
// exit 2; a branch or an issue a slot already holds is blocked.
func (e Env) Adopt(ctx context.Context, root string, be backlog.Backend, o AdoptOpts) (Adopted, error) {
	res := Adopted{Issue: strings.TrimPrefix(strings.TrimSpace(o.Issue), "#"), PR: o.PR}
	if res.Issue == "" {
		return res, usage("--issue is required")
	}
	if strings.TrimSpace(o.Ref) == "" {
		return res, usage("adopt takes a branch or a worktree path")
	}
	branch, wt, err := e.resolveAdoptRef(ctx, root, o.Ref)
	if err != nil {
		return res, err
	}
	res.Branch, res.Worktree = branch, wt
	if err := e.checkAdoptPR(ctx, branch, o.PR); err != nil {
		return res, err
	}

	reg := worker.LoadRegistry(root)
	for _, s := range reg.Slots() {
		if s.Branch() == branch {
			return res, blocked(BlockRegistered, "slot %s already holds %s", s.Name(), branch)
		}
	}
	for _, s := range reg.Slots() {
		if s.HeldID() == strings.ToUpper(res.Issue) {
			return res, blocked(BlockHeld, "slot %s already holds %s", s.Name(), res.Issue)
		}
	}
	name := o.Name
	if name == "" {
		for n := 1; ; n++ {
			if name = "ext-" + strconv.Itoa(n); reg.Slot(name) == nil {
				break
			}
		}
	} else if reg.Slot(name) != nil {
		return res, usage("slot %s already exists", name)
	}
	res.Slot = name

	tracked := e.trackedFiles(ctx, root)
	r, err := Assess(be, res.Issue, tracked, o.Shared, e.InFlightItems(ctx, root, be, tracked, o.Shared), o.AcceptOverlap)
	if err != nil {
		return res, err
	}
	res.Overlaps = r.Overlaps
	if len(r.Overlaps) > 0 && !o.AcceptOverlap {
		blk := blocked(BlockOverlap, "%s overlaps work in flight: %s", res.Issue, strings.Join(r.Checks[len(r.Checks)-1].Detail, "; "))
		blk.Readiness = &r
		return res, blk
	}

	base := e.Base
	if err := worker.RegisterExternal(root, name, branch, wt, base, res.Issue, o.PR); err != nil {
		var rc *worker.RegisterConflict
		if errors.As(err, &rc) { // lost a race: the registry changed since the checks above
			switch rc.Kind {
			case worker.ConflictName:
				return res, usage("slot %s already exists", name)
			case worker.ConflictBranch:
				return res, blocked(BlockRegistered, "slot %s already holds %s", rc.Slot, branch)
			}
			return res, blocked(BlockHeld, "slot %s already holds %s", rc.Slot, res.Issue)
		}
		return res, err
	}
	worker.LedgerNote(root, ledger.Entry{Kind: ledger.KindAdopt, Issue: res.Issue, Slot: name, PR: o.PR, Detail: ledger.Detail("branch", branch)})
	return res, nil
}

// fenceExternal refuses (blockedBy external) a verb that would act on the
// worktree and branch of an adopted slot: rota did not create them. The
// fence on the worker side reads the same predicate, worker.Slot.IsExternal.
// what finishes "<verb> would ..."; a nil or driven slot passes.
func fenceExternal(s *worker.Slot, verb, what string) error {
	if !s.IsExternal() {
		return nil
	}
	return blocked(BlockExternal, "slot %s is an adopted external slot: %s would %s rota did not create", s.Name(), verb, what)
}

// checkAdoptPR validates the PR an adoption records: it must name a PR, the
// forge must know it, and the branch must head it. A slot recording a PR that
// is not its own would be released, gated or reported on someone else's merge.
// No forge, or no forge CLI to ask, skips the read; any other failure to read
// it refuses, since guessing "fine" is what this check is for.
func (e Env) checkAdoptPR(ctx context.Context, branch, pr string) error {
	if pr == "" {
		return nil
	}
	n, ok := worker.PRRefNumber(pr)
	if !ok {
		return usage("--pr %q has no PR number: pass the PR's URL", pr)
	}
	if e.Forge == nil {
		return nil
	}
	info, err := e.Forge.PRView(ctx, n)
	switch {
	case tracker.IsKind(err, tracker.KindNotFound):
		return &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("--pr %s: the forge has no PR %d", pr, n)}
	case tracker.IsKind(err, tracker.KindUnavailable) && strings.Contains(err.Error(), "is not installed"):
		return nil
	case err != nil:
		return fmt.Errorf("--pr %s: could not read PR %d from the forge: %w", pr, n, err)
	case info.Head != branch:
		return &exitcode.Error{Exit: exitcode.ExitResolution, Message: fmt.Sprintf("--pr %s: PR %d is headed by %s, not %s", pr, n, info.Head, branch)}
	}
	return nil
}

// resolveAdoptRef is the branch (and its worktree, "" when none) behind ref: a
// worktree path of this repo, else a local branch.
func (e Env) resolveAdoptRef(ctx context.Context, root, ref string) (branch, wt string, err error) {
	res, gerr := e.Git(ctx, root, "worktree", "list", "--porcelain")
	if gerr != nil || res.ExitCode != 0 {
		return "", "", &exitcode.Error{Exit: exitcode.ExitUnavailable, Message: "git worktree list failed"}
	}
	type entry struct{ path, branch string }
	var all []entry
	for _, l := range strings.Split(res.Stdout, "\n") {
		switch {
		case strings.HasPrefix(l, "worktree "):
			all = append(all, entry{path: realPath(strings.TrimPrefix(l, "worktree "))})
		case strings.HasPrefix(l, "branch ") && len(all) > 0:
			all[len(all)-1].branch = strings.TrimPrefix(strings.TrimPrefix(l, "branch "), "refs/heads/")
		}
	}
	isBranch := func() bool {
		r, _ := e.Git(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+ref)
		return r.ExitCode == 0
	}
	// A path starts with . or /; otherwise a local branch wins over a
	// cwd-relative directory of the same name (branches like codex/12-x contain
	// slashes, so a slash proves nothing).
	asPath := strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, ".")
	if !asPath {
		if fi, serr := os.Stat(ref); serr == nil && fi.IsDir() && !isBranch() {
			asPath = true
		}
	}
	// all[0] is the main checkout: a project root is not adoptable work.
	adoptable := all[min(1, len(all)):]
	if asPath {
		abs := realPath(ref)
		for _, w := range adoptable {
			if w.path != abs {
				continue
			}
			if w.branch == "" {
				return "", "", usage("worktree %s is detached: adopt a branch", ref)
			}
			return e.adoptable(w.branch, w.path)
		}
		return "", "", usage("%s is not a worktree of this repository", ref)
	}
	if !isBranch() {
		return "", "", usage("branch %s not found", ref)
	}
	for _, w := range adoptable {
		if w.branch == ref {
			wt = w.path
		}
	}
	return e.adoptable(ref, wt)
}

// adoptable refuses the base branch and the park/* branches: they are rota's
// own resting places, never someone's work.
func (e Env) adoptable(branch, wt string) (string, string, error) {
	if branch == e.Base || strings.HasPrefix(branch, "park/") {
		return "", "", usage("%s is the base or a parked branch: adopt a work branch", branch)
	}
	return branch, wt, nil
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
