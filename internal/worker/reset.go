package worker

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/tracker"
	"strings"
)

// ResetResult is the outcome of the slot reset guard.
type ResetResult struct {
	Slot     string
	Clean    bool // the slot holds no work
	Retained bool // same task on its own branch: WIP kept, nothing reset
	Branch   string
	Base     string
	SHA      string
	Dirty    []string
	Unmerged []string
	Changed  bool
}

// Reset refuses to reuse a slot that still holds work (#38), otherwise cuts
// a fresh per-task branch from the slot's base. checkOnly stops after the
// guard.
//
// The guard refuses when the worktree has uncommitted changes (untracked files
// included) or commits that never reached the base (`git cherry <base> HEAD`
// shows `+`). The base is the slot's cycle branch, or its fetched origin ref
// when the local branch is behind; `git cherry` compares by patch, so a rebased
// or cherry-picked merge counts as merged and a squash merge does not.
//
// Retry: dispatching the task id the slot already holds, while it sits on that
// task's branch, keeps its WIP (Retained, no reset) instead of refusing it.
//
// A refusal returns an *exitcode.Error (exit 4, or exit 1 with checkOnly) whose Data
// is the ResetResult.
func (e Env) Reset(root, slot, task string, checkOnly bool) (ResetResult, error) {
	return e.ResetTo(root, slot, task, BranchFor(slot, task), checkOnly)
}

// ResetTo is Reset onto an explicit branch: a round slot works on
// `<agent>/<issue>-<slug>` and parks on `park/<agent>`, not rota-worker/….
func (e Env) ResetTo(root, slot, task, newBranch string, checkOnly bool) (ResetResult, error) {
	return e.ResetFrom(root, slot, task, newBranch, "", checkOnly)
}

// ResetFrom is ResetTo with an explicit start point in place of the slot's
// base ("" keeps the base): the support machine cuts from origin/<base>, which
// it has just fetched, and checks for unmerged work against it too.
func (e Env) ResetFrom(root, slot, task, newBranch, from string, checkOnly bool) (ResetResult, error) {
	e = e.WithDefaults()
	res := ResetResult{Slot: slot}
	reg, err := LoadRegistry(root)
	if err != nil {
		return res, err
	}
	if !reg.Exists {
		return res, fail(exitcode.ExitResolution, "no worker pool — run rota worker pool init first")
	}
	s := reg.Slot(slot)
	if s == nil {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", slot))
	}
	if s.IsExternal() {
		return res, ExternalRefusal(slot, "reset")
	}
	worktree, base, oldBranch, oldTask := s.Worktree(), s.Base(), s.Branch(), s.Task()
	if from != "" {
		base = from
	}
	if !fsio.IsDir(worktree) {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' worktree missing: %s", slot, worktree))
	}
	if _, code := e.git(root, "rev-parse", "--verify", "--quiet", base+"^{commit}"); base == "" || code != 0 {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' base '%s' does not exist", slot, base))
	}
	plainBase := base
	if from == "" {
		base, err = e.resetBase(root, slot, base)
		if err != nil {
			return res, err
		}
	}
	res.Base = base

	retry := false
	if task != "" && task == oldTask {
		cur, _ := e.git(worktree, "symbolic-ref", "--short", "-q", "HEAD")
		retry = cur == newBranch
	}

	// A failed status or cherry must not read as "clean": the switch -C below
	// could then orphan unpushed commits. The old helper aborted here (set -e).
	dirty, code := e.git(worktree, "status", "--porcelain")
	if code != 0 {
		return res, fail(exitcode.ExitUnavailable, fmt.Sprintf("git status failed in %s (exit %d); not resetting slot '%s'", worktree, code, slot))
	}
	cherry, code := e.git(worktree, "cherry", base, "HEAD")
	if code != 0 {
		return res, fail(exitcode.ExitUnavailable, fmt.Sprintf("git cherry %s HEAD failed in %s (exit %d); not resetting slot '%s'", base, worktree, code, slot))
	}
	var unmerged []string
	for _, l := range strings.Split(cherry, "\n") {
		if strings.HasPrefix(l, "+ ") {
			unmerged = append(unmerged, l[2:])
		}
	}
	// A squash merge leaves the slot's commits unmerged by patch. A done slot
	// whose merged PR has the slot's HEAD as its head proves they landed.
	var info tracker.PRInfo
	var infoAsked bool
	if len(unmerged) > 0 && dirty == "" && IsDone(s.State()) {
		infoAsked = true
		var known bool
		if info, known = e.prInfoKnown(context.Background(), root, s); known && strings.EqualFold(info.State, "MERGED") && info.HeadSHA != "" {
			if head, code := e.git(worktree, "rev-parse", "HEAD"); code == 0 && head == info.HeadSHA {
				unmerged = nil
			}
		}
	}
	if retry && (dirty != "" || len(unmerged) > 0) {
		res.Retained, res.Branch = true, newBranch
		return res, nil
	}
	refuse := func(msg string) (ResetResult, error) {
		ex := exitcode.ExitRefused
		if checkOnly {
			ex = exitcode.ExitFailed
		}
		err := fail(ex, msg)
		err.Data = res
		return res, err
	}
	cont := oldTask
	if cont == "" {
		cont = "its task"
	}
	if dirty != "" {
		res.Dirty = strings.Split(dirty, "\n")
		return refuse(fmt.Sprintf("REFUSED %s — uncommitted changes in %s: commit or discard them before reusing the slot (or rota worker pool reap %s: %s); re-dispatch %s to continue it in place.\n%s",
			slot, worktree, slot, reapNote(reg, plainBase), cont, indent(res.Dirty)))
	}
	if len(unmerged) > 0 {
		for _, sha := range unmerged {
			line, _ := e.git(worktree, "log", "-1", "--abbrev=7", "--format=%h %s", sha)
			res.Unmerged = append(res.Unmerged, line)
		}
		return refuse(fmt.Sprintf("REFUSED %s — %d commit(s) not on %s: gate and merge them (rota worker gate) before giving the slot another task, or rota worker pool reap %s: %s; re-dispatch %s to continue it in place.\n%s",
			slot, len(unmerged), base, slot, reapNote(reg, plainBase), cont, indent(res.Unmerged)))
	}
	res.Clean = true
	if checkOnly {
		return res, nil
	}

	sw, diverged, err := e.taskBranchSwitch(root, newBranch, base)
	if err != nil {
		return res, err
	}
	if diverged != "" {
		return refuse(diverged)
	}
	if _, code := e.git(worktree, sw...); code != 0 {
		return res, fail(exitcode.ExitUnavailable, fmt.Sprintf("could not switch %s to %s in %s", worktree, newBranch, base))
	}
	if len(sw) > 0 && sw[len(sw)-1] != base {
		// A reused branch keeps its work: pin its upstream when origin has it (best effort).
		if name, rerr := e.Remote(root); rerr == nil {
			if _, code := e.git(root, "rev-parse", "--verify", "--quiet", "refs/remotes/"+name+"/"+newBranch+"^{commit}"); code == 0 {
				e.git(root, "branch", "-q", "--set-upstream-to="+name+"/"+newBranch, newBranch)
			}
		}
	}
	// The old per-task branch was proved merged above; drop it so they don't pile up.
	if strings.HasPrefix(oldBranch, "rota-worker/") && oldBranch != newBranch {
		e.git(root, "branch", "-D", oldBranch)
	}
	// A done slot whose PR merged waits on nothing: ending it here keeps it
	// from reading as needing the orchestrator for good (#1843).
	// The lookup and the switch ran outside the lock: a concurrent assign may
	// have re-bound the slot since, so the slot is re-checked as still done on
	// the same PR before it is wiped.
	seenPR := s.PR()
	endDone := IsDone(s.State())
	if endDone && infoAsked {
		endDone = strings.EqualFold(info.State, "MERGED")
	} else if endDone {
		endDone = e.prMerged(context.Background(), root, s)
	}
	if _, err := UpdateSlot(root, slot, func(s *Slot) {
		s.SetBranch(newBranch)
		if endDone && IsDone(s.State()) && s.PR() == seenPR {
			s.Unbind()
			_ = s.MarkState("idle", stamp.Format(e.Now()))
		}
	}); err != nil {
		return res, err
	}
	res.Branch, res.Changed = newBranch, true
	res.SHA, _ = e.git(root, "rev-parse", "--short", base)
	return res, nil
}

// taskBranchSwitch picks the `git switch` that puts the slot on newBranch
// without losing work (#3797): a branch that already exists (a reclaimed
// worker pushed it, or a local copy is ahead of the base) is reused at its
// furthest tip, and only a missing one is cut from base. A local branch and its
// origin copy that diverged are refused: diverged carries the message.
func (e Env) taskBranchSwitch(root, newBranch, base string) (args []string, diverged string, err error) {
	cut := []string{"switch", "-q", "-C", newBranch, base}
	name, rerr := e.Remote(root)
	if rerr != nil {
		return nil, "", fail(exitcode.ExitRefused, rerr.Error())
	}
	remoteRef := name + "/" + newBranch
	rev := func(ref string) string {
		out, code := e.git(root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if code != 0 {
			return ""
		}
		return out
	}
	// Like resetBase: refresh only a remote-tracking ref that is already known
	// (a push, even from this repo, creates it); a failed refresh keeps the old tip.
	if rev("refs/remotes/"+remoteRef) != "" {
		e.git(root, "fetch", "-q", name, "--", "refs/heads/"+newBranch+":refs/remotes/"+remoteRef)
	}
	local, remote := rev("refs/heads/"+newBranch), rev("refs/remotes/"+remoteRef)
	anc := func(a, b string) (bool, error) {
		ok, aerr := e.isAncestor(root, a, b)
		if aerr != nil {
			return false, fail(exitcode.ExitUnavailable, fmt.Sprintf("could not compare %s with %s before resetting onto it", a, b))
		}
		return ok, nil
	}
	switch {
	case local == "" && remote == "":
		return cut, "", nil
	case local == "":
		return []string{"switch", "-q", "--no-track", "-c", newBranch, remoteRef}, "", nil
	case remote == "" || local == remote:
		merged, aerr := anc(local, base)
		if aerr != nil {
			return nil, "", aerr
		}
		if merged && remote == "" {
			return cut, "", nil // nothing on it the base lacks
		}
		return []string{"switch", "-q", newBranch}, "", nil
	}
	if ahead, aerr := anc(remote, local); aerr != nil {
		return nil, "", aerr
	} else if ahead {
		return []string{"switch", "-q", newBranch}, "", nil
	}
	if behind, aerr := anc(local, remote); aerr != nil {
		return nil, "", aerr
	} else if behind {
		return []string{"switch", "-q", "-C", newBranch, remoteRef}, "", nil
	}
	return nil, fmt.Sprintf("REFUSED %s — local %s and %s diverged: merge or reset one onto the other by hand before assigning the issue", newBranch, newBranch, remoteRef), nil
}

// resetBase refreshes a tracked base before judging the slot's commits. A
// support machine's local main can lag landed work. Local cycle branches
// without an origin counterpart, or ahead of origin, retain their own base.
func (e Env) resetBase(root, slot, base string) (string, error) {
	name, err := e.Remote(root)
	if err != nil {
		return "", fail(exitcode.ExitRefused, err.Error())
	}
	remote := name + "/" + base
	if _, code := e.git(root, "rev-parse", "--verify", "--quiet", "refs/remotes/"+remote+"^{commit}"); code != 0 {
		return base, nil
	}
	if _, code := e.git(root, "fetch", "-q", name, base); code != 0 {
		return "", fail(exitcode.ExitUnavailable, fmt.Sprintf("could not fetch %s before checking slot '%s'; not resetting it", remote, slot))
	}
	behind, err := e.isAncestor(root, base, remote)
	if err != nil {
		return "", fail(exitcode.ExitUnavailable, fmt.Sprintf("could not compare %s with %s before resetting slot '%s'", base, remote, slot))
	}
	if behind {
		return remote, nil
	}
	return base, nil
}

// reapNote says what `pool reap` costs a slot and how to get one back. A
// round's slots are re-provisioned by `round start`; `pool init` creates the
// pool's w1..wN, never a reaped roster name, so it is named only outside a
// round. base is the plain branch name, without a remote prefix.
func reapNote(reg Registry, base string) string {
	const gone = "it removes the slot, its worktree and its branch"
	if reg.SlotCount() > 0 {
		return gone + "; rota round start re-provisions the slot before it takes another task"
	}
	return gone + "; rota worker pool init --slots <n> --base " + base + " creates slots again before one takes another task"
}

func indent(lines []string) string {
	return "  " + strings.Join(lines, "\n  ")
}
