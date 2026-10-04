package round

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/worker"
)

// Parked is what Park did to a slot's worktree.
type Parked struct {
	Branch   string // the work branch the slot was on (or recorded)
	Head     string // sha7 and subject of its tip, "" when it has none
	Salvaged bool   // a salvage commit was made
	Moved    bool   // the worktree was switched to park/<agent>
}

func (e Env) gitOut(ctx context.Context, dir string, args ...string) (string, string, int) {
	out, errOut, code, err := e.Git(ctx, dir, args...)
	if err != nil {
		return "", err.Error(), 127
	}
	return strings.TrimRight(out, "\n"), strings.TrimSpace(errOut), code
}

func unavailable(format string, a ...any) error {
	return &worker.Error{Exit: worker.ExitUnavailable, Message: fmt.Sprintf(format, a...)}
}

// dirtyPaths lists the worktree's changed paths by name: modified, deleted,
// added and untracked (a new directory by its own name), a rename by its new
// name.
func (e Env) dirtyPaths(ctx context.Context, wt string) ([]string, error) {
	out, errOut, code, err := e.Git(ctx, wt, "status", "--porcelain", "-z")
	if err != nil || code != 0 {
		return nil, unavailable("git status failed in %s: %s", wt, strings.TrimSpace(errOut))
	}
	var paths []string
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if len(p) < 4 {
			continue
		}
		paths = append(paths, p[3:])
		if p[0] == 'R' || p[0] == 'C' || p[1] == 'R' || p[1] == 'C' {
			i++ // the original name follows
		}
	}
	return paths, nil
}

// Park frees a slot's worktree without losing its work, the sequence return,
// transfer and reclaim share. It salvages the worktree when dirty (stages the
// dirty paths by name, never -A, and commits `wip: parked from <slot> (rota
// round <verb>)`), pushes the work branch to origin (`git push -u origin
// <branch>`, no force) and only after the push succeeded switches the worktree
// to park/<agent> at the base. A failed push or a rejected commit leaves the
// slot as found (exit 5): a salvage commit already made is taken back.
//
// A slot already off its work branch (a repeated call) only pushes again; a
// slot with no work branch is a no-op.
func (e Env) Park(ctx context.Context, root, name, verb string) (Parked, error) {
	var p Parked
	s := worker.LoadRegistry(root).Slot(name)
	if s == nil {
		return p, &worker.Error{Exit: worker.ExitResolution, Message: fmt.Sprintf("slot %s is not in the pool", name)}
	}
	wt := s.Worktree()
	if fi, err := os.Stat(wt); wt == "" || err != nil || !fi.IsDir() {
		return p, &worker.Error{Exit: worker.ExitResolution, Message: fmt.Sprintf("slot %s worktree missing: %s", name, wt)}
	}
	base := firstNonEmpty(s.Base(), e.Base)
	parkBr := "park/" + name
	cur, _, _ := e.gitOut(ctx, wt, "symbolic-ref", "--short", "-q", "HEAD")
	branch := firstNonEmpty(s.Branch(), cur)
	p.Branch = branch
	headOf := func(ref string) string {
		out, _, code := e.gitOut(ctx, wt, "log", "-1", "--abbrev=7", "--format=%h %s", ref, "--")
		if code != 0 {
			return ""
		}
		return out
	}
	if branch == "" || branch == parkBr || branch == base {
		p.Head = headOf("HEAD")
		return p, nil
	}
	if _, _, code := e.gitOut(ctx, wt, "rev-parse", "--verify", "-q", "refs/heads/"+branch); code != 0 {
		return p, unavailable("branch %s of slot %s does not exist", branch, name)
	}

	if cur == branch {
		dirty, err := e.dirtyPaths(ctx, wt)
		if err != nil {
			return p, err
		}
		if len(dirty) > 0 {
			args := append([]string{"add", "--"}, dirty...)
			if _, errOut, code := e.gitOut(ctx, wt, args...); code != 0 {
				e.gitOut(ctx, wt, "reset", "-q")
				return p, unavailable("could not stage %s's changes: %s", name, errOut)
			}
			msg := fmt.Sprintf("wip: parked from %s (rota round %s)", name, verb)
			if _, errOut, code := e.gitOut(ctx, wt, "commit", "-q", "-m", msg); code != 0 {
				e.gitOut(ctx, wt, "reset", "-q")
				return p, unavailable("salvage commit in %s was rejected: %s", name, errOut)
			}
			p.Salvaged = true
		}
	}
	if _, errOut, code := e.gitOut(ctx, wt, "push", "-u", "origin", branch); code != 0 {
		if p.Salvaged {
			e.gitOut(ctx, wt, "reset", "-q", "--mixed", "HEAD~1")
			p.Salvaged = false
		}
		return p, unavailable("git push origin %s failed, slot %s left as found: %s", branch, name, errOut)
	}
	p.Head = headOf(branch)
	if cur == branch {
		if _, errOut, code := e.gitOut(ctx, wt, "switch", "-q", "-C", parkBr, base); code != 0 {
			return p, unavailable("could not switch %s to %s at %s: %s", name, parkBr, base, errOut)
		}
		p.Moved = true
	}
	return p, nil
}

// StallInput is one slot as Stalled judges it.
type StallInput struct {
	Worktree, Base string
	Holds          bool   // the slot holds an issue
	Alive          bool   // its host agent is alive
	Escalated      bool   // an open escalation waits on it
	ActiveAt       string // the registry's activeAt, RFC 3339 UTC, "" when absent
	Minutes        int    // round.stallMinutes; 0 turns the check off
}

// Stall is Stalled's verdict.
type Stall struct {
	Stalled bool
	Idle    time.Duration
	// Signal names what moved last: "commit", "uncommitted edit" or "state change".
	Signal string
}

// Stalled says whether a slot has made no progress for in.Minutes. Moved is the
// newest of: the committer time of the branch tip when it has commits past the
// base, the newest mtime among the worktree's dirty paths, and activeAt. A slot
// that holds no issue, whose agent is not alive (that is `dead`, not stalled),
// or that waits on an escalation is never stalled; neither is one with no
// signal at all. now is injectable so the threshold needs no sleeping.
func (e Env) Stalled(ctx context.Context, in StallInput, now time.Time) Stall {
	var st Stall
	if in.Minutes <= 0 || !in.Holds || !in.Alive || in.Escalated {
		return st
	}
	var last time.Time
	note := func(t time.Time, signal string) {
		if t.After(last) {
			last, st.Signal = t, signal
		}
	}
	if in.Worktree != "" {
		ref := in.Base
		if _, _, code := e.gitOut(ctx, in.Worktree, "rev-parse", "--verify", "-q", "origin/"+in.Base); in.Base != "" && code == 0 {
			ref = "origin/" + in.Base
		}
		if ref != "" {
			if n, _, code := e.gitOut(ctx, in.Worktree, "rev-list", "--count", ref+"..HEAD"); code == 0 && n != "0" && n != "" {
				if ts, _, code := e.gitOut(ctx, in.Worktree, "log", "-1", "--format=%ct", "HEAD"); code == 0 {
					if sec, err := strconv.ParseInt(ts, 10, 64); err == nil {
						note(time.Unix(sec, 0), "commit")
					}
				}
			}
		}
		if dirty, err := e.dirtyPaths(ctx, in.Worktree); err == nil {
			for _, p := range dirty {
				if fi, err := os.Stat(filepath.Join(in.Worktree, p)); err == nil {
					note(fi.ModTime(), "uncommitted edit")
				}
			}
		}
	}
	if t, err := time.Parse(time.RFC3339, in.ActiveAt); err == nil {
		note(t, "state change")
	}
	if last.IsZero() {
		return st
	}
	st.Idle = now.Sub(last)
	st.Stalled = st.Idle >= time.Duration(in.Minutes)*time.Minute
	return st
}
