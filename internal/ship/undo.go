// Package ship holds the rules behind `rota ship undo`: which merge it may
// roll back and what must refuse first. The CLI owns flags, envelopes and
// exit codes; this package decides, and resets only in ApplyUndo.
package ship

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/pystr"
)

// Git is the one git call the undo rules need. A non-zero exit is a Result;
// err is a failure to run git at all.
type Git interface {
	Run(args ...string) (git.Result, error)
}

// Refusal is a guard that stopped the undo before it changed anything. By
// names the guard (the envelope's blockedBy).
type Refusal struct {
	By, Msg, Hint string
}

func (r *Refusal) Error() string { return r.Msg }

// NotFoundError is a cycle that cannot be resolved to a commit.
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string { return e.Msg }

// GitError is a git call that exited non-zero where the rules needed it to
// succeed.
type GitError struct{ Msg string }

func (e *GitError) Error() string { return e.Msg }

// PartialError means the reset already happened and the restore failed after.
type PartialError struct {
	Head string // short HEAD after the reset
	Err  error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("the reset already happened (HEAD is now %s), but restoring an item failed: %v", e.Head, e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

// UndoOpts are the caller's choices for PlanUndo.
type UndoOpts struct {
	Cycle     string // merge commit to undo; "" means HEAD must be the merge
	AllowPost bool   // permit commits made after the merge
	// IDs lists the item IDs whose done lines carry one of the cycle's short
	// hashes. Nil means none; the backlog stays with the caller.
	IDs func(hashes map[string]bool) []string
}

// UndoPlan is what an undo would do, resolved and checked.
type UndoPlan struct {
	Base      string
	Merge     string // full hash of the cycle merge
	Short     string
	Subject   string
	PreMerge  string // first parent: where the base resets to
	PreShort  string
	PostCount int // first-parent commits after the merge
	IDs       []string
}

// PlanUndo resolves the cycle merge on base and applies every guard, running
// only read-only git. cur is the checked-out branch ("" when detached) and
// dirty reports uncommitted changes; the caller reads both.
func PlanUndo(g Git, base, cur string, dirty bool, o UndoOpts) (UndoPlan, error) {
	if cur != base {
		if cur == "" {
			cur = "(detached HEAD)"
		}
		return UndoPlan{}, &Refusal{By: "not on base branch", Msg: fmt.Sprintf("must run on the base branch (%s), currently on %s", base, cur)}
	}
	if dirty {
		return UndoPlan{}, &Refusal{By: "dirty tree", Msg: "uncommitted changes: stash (`git stash`) or commit before running ship undo"}
	}

	// With no cycle, HEAD itself must be the merge.
	if o.Cycle == "" {
		parents, err := out(g, "rev-list", "--parents", "-1", "HEAD")
		if err != nil {
			return UndoPlan{}, err
		}
		switch n := len(strings.Fields(parents)); n {
		case 3:
		case 2:
			return UndoPlan{}, &Refusal{By: "not a merge", Msg: "HEAD is not a merge commit; undo is not supported for this shape",
				Hint: "revert it instead: git revert HEAD (squash- and rebase-merges leave one parent)"}
		default:
			return UndoPlan{}, &Refusal{By: "not a merge", Msg: fmt.Sprintf("unexpected HEAD shape (%d parents); manual investigation required", n-1)}
		}
	}

	var merge string
	if o.Cycle != "" {
		res, err := g.Run("rev-parse", "--verify", o.Cycle+"^{commit}")
		if err != nil {
			return UndoPlan{}, err
		}
		if res.Code != 0 {
			return UndoPlan{}, &NotFoundError{Msg: fmt.Sprintf("--cycle hash '%s' is not a valid commit", o.Cycle)}
		}
		merge = line(res.Stdout)
		parents, err := out(g, "rev-list", "--parents", "-n", "1", merge)
		if err != nil {
			return UndoPlan{}, err
		}
		if len(strings.Fields(parents)) < 3 {
			return UndoPlan{}, &Refusal{By: "not a merge", Msg: fmt.Sprintf("--cycle commit %s is not a merge commit", o.Cycle)}
		}
	} else {
		var err error
		if merge, err = out(g, "log", "--first-parent", "--merges", "-1", "--pretty=%H", base); err != nil {
			merge = ""
		}
		if merge == "" {
			return UndoPlan{}, &NotFoundError{Msg: fmt.Sprintf("no merge commit found on %s", base)}
		}
	}
	subject, err := out(g, "log", "-1", "--pretty=%s", merge)
	if err != nil {
		return UndoPlan{}, err
	}
	if !strings.HasPrefix(subject, "merge: ") {
		if o.Cycle != "" {
			return UndoPlan{}, &Refusal{By: "merge subject", Msg: fmt.Sprintf("--cycle commit %s has subject not matching '^merge: ' (subject: %s)", o.Cycle, subject)}
		}
		return UndoPlan{}, &Refusal{By: "merge subject", Msg: fmt.Sprintf("most recent merge on %s is not a rota cycle merge (subject: %s)", base, subject)}
	}

	p := UndoPlan{Base: base, Merge: merge, Subject: subject}
	if p.Short, err = out(g, "rev-parse", "--short", merge); err != nil {
		return UndoPlan{}, err
	}
	if p.PreMerge, err = out(g, "rev-parse", merge+"^1"); err != nil {
		return UndoPlan{}, err
	}
	if p.PreShort, err = out(g, "rev-parse", "--short", p.PreMerge); err != nil {
		return UndoPlan{}, err
	}
	tip, err := out(g, "rev-parse", merge+"^2")
	if err != nil {
		return UndoPlan{}, err
	}

	post, _ := out(g, "log", "--first-parent", "--oneline", merge+".."+base)
	if post != "" {
		p.PostCount = len(strings.Split(post, "\n"))
	}
	if p.PostCount > 0 && !o.AllowPost {
		return UndoPlan{}, &Refusal{By: "post-merge commits", Msg: fmt.Sprintf("%d commit(s) on %s after the cycle merge", p.PostCount, base),
			Hint: "pass --allow-post-merge to discard them, or reset manually first"}
	}

	// A remote ref at the cycle tip means the cycle went through a PR.
	if refs, _ := out(g, "for-each-ref", "--format=%(refname:short)", "--points-at", tip, "refs/remotes/"); refs != "" {
		return UndoPlan{}, &Refusal{By: "pr mode", Msg: fmt.Sprintf("PR-mode cycle: rolling back upstream PRs is manual (gh pr close / git revert); remote ref(s) at the cycle tip: %s",
			strings.Join(strings.Split(refs, "\n"), ", "))}
	}

	hashes := map[string]bool{}
	hs, _ := out(g, "log", "--pretty=%h", merge+"^1.."+merge+"^2")
	for _, h := range strings.Split(hs, "\n") {
		if h = pystr.Strip(h); h != "" {
			hashes[h] = true
		}
	}
	if o.IDs != nil {
		p.IDs = o.IDs(hashes)
	}
	return p, nil
}

// ApplyUndo resets the base branch to the commit before the merge, then
// calls restore with the plan's item IDs. The caller has already checked out
// base. A restore failure after the reset is a *PartialError.
func ApplyUndo(g Git, p UndoPlan, restore func(ids []string) error) error {
	if _, err := out(g, "reset", "--hard", p.Merge+"^1"); err != nil {
		return err
	}
	if len(p.IDs) > 0 && restore != nil {
		if err := restore(p.IDs); err != nil {
			head, _ := out(g, "rev-parse", "--short", "HEAD")
			return &PartialError{Head: head, Err: err}
		}
	}
	return nil
}

// out runs git and returns stdout without trailing newlines; a non-zero exit
// is a *GitError.
func out(g Git, args ...string) (string, error) {
	res, err := g.Run(args...)
	if err == nil && res.Code != 0 {
		err = &GitError{Msg: fmt.Sprintf("git %s: %s", strings.Join(args, " "), firstLine(res.Stderr))}
	}
	return line(res.Stdout), err
}

func line(s string) string { return strings.TrimRight(s, "\n") }

func firstLine(s string) string {
	for _, l := range pystr.Splitlines(s) {
		if l = pystr.Strip(l); l != "" {
			return l
		}
	}
	return ""
}
