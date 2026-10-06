package ship

import (
	"os"
	"path/filepath"
	"strings"
)

// ClearWorktree removes a clean cycle worktree before a local merge. The
// caller must establish ownership and exclude round slots through check.
// Never remove the caller's cwd, even when it is a subdirectory or symlink.
func ClearWorktree(g Git, branch string, check func(path string) error) error {
	res, err := g.Run("worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return &GitError{Msg: "git worktree list: " + firstLine(res.Stderr)}
	}
	wt := linkedWorktree(res.Stdout, branch)
	if wt == "" {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if PathContains(wt, cwd) {
		return &Refusal{By: "worktree", Msg: "worktree contains the current directory: " + wt, Hint: "run ship merge from outside this worktree"}
	}
	if check == nil {
		return &Refusal{By: "worktree", Msg: "worktree cleanup ownership is unknown: " + wt}
	}
	if err := check(wt); err != nil {
		return err
	}
	// Include ignored files: git worktree remove otherwise silently deletes
	// ignored scratch files. Never use --force, including after this check.
	dirty, err := g.Run("-C", wt, "status", "--porcelain", "--untracked-files=all", "--ignored")
	if err != nil {
		return err
	}
	if dirty.ExitCode != 0 {
		return &GitError{Msg: "git status " + wt + ": " + firstLine(dirty.Stderr)}
	}
	if strings.TrimSpace(dirty.Stdout) != "" {
		return &Refusal{By: "worktree", Msg: "worktree has uncommitted or ignored files: " + wt, Hint: "preserve the files before retrying ship merge"}
	}
	rm, err := g.Run("worktree", "remove", wt)
	if err != nil {
		return err
	}
	if rm.ExitCode != 0 {
		return &GitError{Msg: "git worktree remove " + wt + ": " + firstLine(rm.Stderr)}
	}
	return nil
}

// PathContains compares physical paths, with component boundaries (wt-2 is
// not inside wt). Relative paths are resolved against the process cwd.
func PathContains(parent, child string) bool {
	clean := func(p string) string {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		return filepath.Clean(p)
	}
	rel, err := filepath.Rel(clean(parent), clean(child))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// linkedWorktree is the path of the linked (not the first) worktree in a
// porcelain listing that has branch checked out, "" if none.
func linkedWorktree(porcelain, branch string) string {
	wt, count := "", 0
	for _, l := range strings.Split(porcelain, "\n") {
		if p, ok := strings.CutPrefix(l, "worktree "); ok {
			wt = p
			count++
		} else if l == "branch refs/heads/"+branch && count > 1 {
			return wt
		}
	}
	return ""
}
