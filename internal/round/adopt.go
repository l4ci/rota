package round

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// Blocked reasons of an adoption (exit 4, `blockedBy`).
const (
	// BlockRegistered: a slot already holds the branch.
	BlockRegistered = blockedby.Registered
	// BlockHeld: a slot already holds the issue.
	BlockHeld = blockedby.Held
	// BlockExternal: a destructive verb was pointed at an adopted slot, whose
	// worktree and branch rota did not create.
	BlockExternal = blockedby.External
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
	Warnings                          []string
}

// Adopt registers work another tool started as a hostless slot. It runs the
// overlap check `round assign` runs and refuses a blocking clash unless
// o.AcceptOverlap. A path outside this repo's worktrees or an unknown branch is
// exit 2; a branch or an issue a slot already holds is blocked.
func (e Env) Adopt(ctx context.Context, root string, be backlog.Backend, o AdoptOpts) (Adopted, error) {
	res := Adopted{Issue: worker.NormID(o.Issue), PR: o.PR}
	if res.Issue == "" {
		return res, usage("--issue is required")
	}
	if strings.TrimSpace(o.Ref) == "" {
		return res, usage("adopt takes a branch or a worktree path")
	}
	if err := e.baseCfgErr(); err != nil {
		return res, err
	}
	branch, wt, err := e.resolveAdoptRef(ctx, root, o.Ref)
	if err != nil {
		return res, err
	}
	res.Branch, res.Worktree = branch, wt
	if err := e.checkAdoptPR(ctx, branch, o.PR); err != nil {
		return res, err
	}

	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return res, err
	}
	if s, _ := reg.BranchHolder(branch); s != nil {
		return res, blocked(BlockRegistered, "slot %s already holds %s", s.Name(), branch)
	}
	for _, s := range reg.Slots() {
		if s.HeldID() == worker.NormID(res.Issue) {
			return res, blocked(BlockHeld, "slot %s already holds %s", s.Name(), res.Issue)
		}
	}
	// A fresh claim by another worker can coexist with an orchestrator's
	// fix-forward. Refuse only positive evidence of stale or reclaimed work.
	if board, ok := BoardOf(be); ok && be.Capabilities().ClaimWrites {
		warnings, err := e.checkAdoptClaim(ctx, root, board, branch, res.Issue)
		res.Warnings = append(res.Warnings, warnings...)
		if err != nil {
			return res, err
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
		var detail []string
		for _, check := range r.Checks {
			if check.Name == CheckOverlap {
				detail = check.Detail
			}
		}
		blk := blocked(BlockOverlap, "%s overlaps work in flight: %s", res.Issue, strings.Join(detail, "; "))
		blk.Readiness = &r
		return res, blk
	}

	base := e.Base
	if res.PR == "" && e.Forge != nil { // record the branch's open PR now: train reads the slot, not the forge
		res.PR, err = e.openBranchPR(ctx, branch)
		if err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("open PRs of %s: %v; adopted without a PR, retry with `rota round reconcile --apply`", branch, err))
		}
	}
	if err := worker.RegisterExternal(root, name, branch, wt, base, res.Issue, res.PR); err != nil {
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
	e.Worker.LedgerNote(root, ledger.Entry{Kind: ledger.KindAdopt, Issue: res.Issue, Slot: name, PR: res.PR, Detail: ledger.Detail("branch", branch)})
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
	return blocked(BlockExternal, "%s", worker.ExternalMessage(s.Name(), verb, what))
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
	case tracker.IsCLIMissing(err):
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
	for _, w := range git.ParseWorktrees(res.Stdout) {
		all = append(all, entry{path: realPath(w.Path), branch: w.Branch})
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
	if branch == e.Base || worker.IsPark(branch) {
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

// checkAdoptClaim protects reclaimed work without inferring ownership from a
// branch name alone. Raw marker history comes from the issue source, which can
// differ from the PR forge. An unavailable history cannot prove a takeover.
func (e Env) checkAdoptClaim(ctx context.Context, root string, be Board, branch, issue string) ([]string, error) {
	st, err := be.Status(issue)
	if err != nil {
		return []string{fmt.Sprintf("adopt claim check on %s skipped: %v", issue, err)}, nil
	}
	if st == nil {
		return nil, nil
	}
	held := openClaimsOf(st)
	if len(held) == 0 {
		return nil, nil
	}
	agent, _, _ := strings.Cut(branch, "/")
	for _, id := range held {
		holder, _, _ := strings.Cut(id, "@")
		if agent != "" && holder == agent {
			return nil, nil
		}
	}
	key, ok := tracker.ParseKey(issue)
	if !ok {
		return nil, nil
	}
	stale, warnings := e.StaleClaims(ctx, root, be, []tracker.IssueKey{key})
	for _, claim := range stale {
		if slices.Contains(held, claim.Holder) {
			return warnings, blocked(BlockClaimed, "%s has stale claim %s; branch %s cannot be adopted without reclaiming it", issue, claim.Holder, branch)
		}
	}
	source, ok := be.(*backlog.Issues)
	if !ok || source.Tracker == nil {
		return append(warnings, fmt.Sprintf("adopt claim history on %s unavailable; reclaimed-branch check skipped", issue)), nil
	}
	comments, err := source.Tracker.Comments(ctx, key)
	if err != nil {
		return append(warnings, fmt.Sprintf("adopt claim history on %s skipped: %v", issue, err)), nil
	}
	if reclaimedBranchClaim(comments, agent, held) {
		return warnings, blocked(BlockClaimed, "%s was reclaimed from %s and is claimed by %s; branch %s cannot be adopted", issue, agent, strings.Join(held, ", "), branch)
	}
	return warnings, nil
}

// reclaimedBranchClaim requires a branch-agent claim, its release, then a
// different still-open claim, in that order. A bare release or an existing
// best-of peer is not evidence that the branch was reclaimed.
func reclaimedBranchClaim(comments []tracker.Comment, agent string, held []string) bool {
	claimed := map[string]bool{}
	released := false
	for _, comment := range comments {
		verb, id, ok := marker.ParseClaim(comment.Body)
		if !ok {
			continue
		}
		holder, _, _ := strings.Cut(id, "@")
		if holder == agent && agent != "" {
			if verb == marker.KindClaim {
				claimed[id] = true
			}
			if verb == marker.KindRelease && claimed[id] {
				delete(claimed, id)
				released = true
			}
		} else if verb == marker.KindClaim && released && slices.Contains(held, id) {
			return true
		}
	}
	return false
}
