package worker

import (
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
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

// BranchFor is the per-task branch name: rota-worker/<slot>-<task> with the task
// lowercased and every byte outside [a-z0-9._-] turned into '-' (tr, so a
// multi-byte character becomes several), or rota-worker/<slot> without a task.
// (Not rota-worker/<slot>/<task>: git cannot hold both refs.)
func BranchFor(slot, task string) string {
	if task == "" {
		return "rota-worker/" + slot
	}
	b := []byte(task)
	for i, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			b[i] = c + 32
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			b[i] = '-'
		}
	}
	return "rota-worker/" + slot + "-" + string(b)
}

// Reset is the slot reset guard of bin/hv-worker-reset (#38): refuse to reuse
// a slot that still holds work, otherwise cut a fresh per-task branch from
// the slot's base. checkOnly stops after the guard.
//
// The guard refuses when the worktree has uncommitted changes (untracked files
// included) or commits that never reached the base (`git cherry <base> HEAD`
// shows `+`). The base is the slot's cycle branch, which is local and where
// the gate lands finished work; `git cherry` compares by patch, so a rebased
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
	e = e.withDefaults()
	res := ResetResult{Slot: slot}
	reg := LoadRegistry(root)
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
	if !isDir(worktree) {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' worktree missing: %s", slot, worktree))
	}
	if _, code := e.git(root, "rev-parse", "--verify", "--quiet", base+"^{commit}"); base == "" || code != 0 {
		return res, fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' base '%s' does not exist", slot, base))
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
		return refuse(fmt.Sprintf("REFUSED %s — uncommitted changes in %s: commit or discard them (or rota worker pool reap %s) before reusing the slot; re-dispatch %s to continue it in place.\n%s",
			slot, worktree, slot, cont, indent(res.Dirty)))
	}
	if len(unmerged) > 0 {
		for _, sha := range unmerged {
			line, _ := e.git(worktree, "log", "-1", "--abbrev=7", "--format=%h %s", sha)
			res.Unmerged = append(res.Unmerged, line)
		}
		return refuse(fmt.Sprintf("REFUSED %s — %d commit(s) not on %s: gate and merge them (rota worker gate), or rota worker pool reap %s, before giving the slot another task; re-dispatch %s to continue it in place.\n%s",
			slot, len(unmerged), base, slot, cont, indent(res.Unmerged)))
	}
	res.Clean = true
	if checkOnly {
		return res, nil
	}

	if _, code := e.git(worktree, "switch", "-q", "-C", newBranch, base); code != 0 {
		return res, fail(exitcode.ExitUnavailable, fmt.Sprintf("could not cut %s from %s in %s", newBranch, base, worktree))
	}
	// The old per-task branch was proved merged above; drop it so they don't pile up.
	if strings.HasPrefix(oldBranch, "rota-worker/") && oldBranch != newBranch {
		e.git(root, "branch", "-D", oldBranch)
	}
	if _, err := UpdateSlot(root, slot, func(s *Slot) { s.SetBranch(newBranch) }); err != nil {
		return res, err
	}
	res.Branch, res.Changed = newBranch, true
	res.SHA, _ = e.git(root, "rev-parse", "--short", base)
	return res, nil
}

func indent(lines []string) string {
	return "  " + strings.Join(lines, "\n  ")
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return p != "" && err == nil && fi.IsDir()
}
