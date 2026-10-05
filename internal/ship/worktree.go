package ship

import "strings"

// ClearWorktree removes the linked worktree that has branch checked out, so
// the branch can be pushed or merged and deleted. onDisk, when set, names the
// worktree a layout-B umbrella keeps under its root and reports "" when it is
// not there; it is the fallback when git does not list one.
func ClearWorktree(g Git, branch string, onDisk func() string) error {
	res, err := g.Run("worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	wt := linkedWorktree(res.Stdout, branch)
	if wt == "" && onDisk != nil {
		wt = onDisk()
	}
	if wt == "" {
		return nil
	}
	rm, err := g.Run("worktree", "remove", wt)
	if err != nil {
		return err
	}
	if rm.Code != 0 {
		return &GitError{Msg: "git worktree remove " + wt + ": " + firstLine(rm.Stderr)}
	}
	return nil
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
