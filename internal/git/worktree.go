package git

import "strings"

// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
	Path, Head, Branch string
	Bare, Detached     bool
	Locked, Prunable   bool
	PrunableReason     string // git's reason, when Prunable
}

// ParseWorktrees parses `git worktree list --porcelain` output into one record
// per entry, in git's order (the first is the main checkout). Branch has the
// refs/heads/ prefix stripped; Path is git's own, uncleaned.
func ParseWorktrees(porcelain string) []Worktree {
	var out []Worktree
	for _, line := range strings.Split(porcelain, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			out = append(out, Worktree{Path: p})
			continue
		}
		if len(out) == 0 {
			continue
		}
		w := &out[len(out)-1]
		switch {
		case strings.HasPrefix(line, "HEAD "):
			w.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			w.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "bare":
			w.Bare = true
		case line == "detached":
			w.Detached = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			w.Locked = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			w.Prunable = true
			w.PrunableReason = strings.TrimSpace(strings.TrimPrefix(line, "prunable"))
		}
	}
	return out
}
