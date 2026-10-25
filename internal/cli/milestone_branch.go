package cli

import (
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	ms "github.com/l4ci/rota/internal/milestone"
)

// milestoneBranch is the long-lived branch a milestone's work merges into.
func milestoneBranch(id string) string { return "milestone/" + id }

// milestoneArchiveBranch is where a kept milestone branch goes on archive.
func milestoneArchiveBranch(id string) string { return "archive/milestone/" + id }

// milestoneBranchSteps are the branch operations of one status change: before
// runs ahead of the status write and a failure there leaves the status as it
// was; after runs once the status is written. Either may be nil.
type milestoneBranchSteps struct {
	before, after func() error
}

// planMilestoneBranch plans the branch work of moving id to the status to,
// refusing what must not start (an unanswered keep/delete, a base that does
// not resolve) before anything changes. A project with no origin remote, or
// no git repository, has no branch to manage: the plan is empty.
func planMilestoneBranch(c *Ctx, st ms.Store, id, to, choice string) (milestoneBranchSteps, error) {
	dir, err := gitDir(c)
	if err != nil || dir == "" {
		return milestoneBranchSteps{}, nil
	}
	ctx := c.Context()
	r := c.deps().Repo(dir)
	if in, err := r.IsRepo(ctx); err != nil || !in {
		return milestoneBranchSteps{}, gitErr(err)
	}
	if ok, err := r.Ok(ctx, "remote", "get-url", "origin"); err != nil || !ok {
		return milestoneBranchSteps{}, gitErr(err)
	}
	branch := milestoneBranch(id)
	exists := func() (bool, error) {
		res, err := r.Run(ctx, "ls-remote", "--exit-code", "--heads", "origin", "refs/heads/"+branch)
		switch {
		case err != nil:
			return false, gitErr(err)
		case res.ExitCode == 0:
			return true, nil
		case res.ExitCode == 2:
			return false, nil
		}
		return false, Unavailable("cannot read origin for %s: %s", branch, strings.TrimSpace(res.Stderr))
	}
	push := func(src, dst string) error {
		res, err := r.Run(ctx, "push", "origin", src+":refs/heads/"+dst)
		if err != nil {
			return gitErr(err)
		}
		if res.ExitCode != 0 {
			return Refused("could not push %s to origin: %s", dst, strings.TrimSpace(res.Stderr))
		}
		return nil
	}
	remove := func() error {
		if has, err := exists(); err != nil || !has {
			return err
		}
		res, err := r.Run(ctx, "push", "origin", "--delete", branch)
		if err != nil {
			return gitErr(err)
		}
		if res.ExitCode != 0 {
			return Refused("could not delete %s on origin: %s", branch, strings.TrimSpace(res.Stderr))
		}
		return nil
	}
	fetch := func() error {
		res, err := r.Run(ctx, "fetch", "-q", "origin", "refs/heads/"+branch)
		if err != nil {
			return gitErr(err)
		}
		if res.ExitCode != 0 {
			return Unavailable("could not fetch %s from origin: %s", branch, strings.TrimSpace(res.Stderr))
		}
		return nil
	}

	switch to {
	case "active":
		base, ok, err := resolveBase(ctx, c.deps(), dir)
		if err != nil {
			return milestoneBranchSteps{}, err
		}
		if !ok {
			return milestoneBranchSteps{}, Resolution(noBaseMsg)
		}
		return milestoneBranchSteps{before: func() error {
			if has, err := exists(); err != nil || has {
				return err
			}
			return push(base, branch)
		}}, nil
	case "shipped":
		return milestoneBranchSteps{after: remove}, nil
	case "archived":
		has, err := exists()
		if err != nil {
			return milestoneBranchSteps{}, err
		}
		if !has {
			return milestoneBranchSteps{}, nil
		}
		switch choice {
		case "delete":
			return milestoneBranchSteps{after: remove}, nil
		case "keep":
			return milestoneBranchSteps{after: func() error {
				if err := fetch(); err != nil {
					return err
				}
				if err := push("FETCH_HEAD", milestoneArchiveBranch(id)); err != nil {
					return err
				}
				return remove()
			}}, nil
		}
		return milestoneBranchSteps{}, Usage("%s has a branch on origin: pass --branch keep (renames it %s) or --branch delete", id, milestoneArchiveBranch(id))
	case "planned":
		cur, err := storedMilestoneStatus(st, id)
		if err != nil || cur != "active" {
			return milestoneBranchSteps{}, err
		}
		has, err := exists()
		if err != nil || !has {
			return milestoneBranchSteps{}, err
		}
		base, ok, err := resolveBase(ctx, c.deps(), dir)
		if err != nil {
			return milestoneBranchSteps{}, err
		}
		if !ok {
			return milestoneBranchSteps{}, Resolution(noBaseMsg)
		}
		return milestoneBranchSteps{before: func() error {
			if err := fetch(); err != nil {
				return err
			}
			res, err := r.Run(ctx, "rev-list", "--count", base+"..FETCH_HEAD")
			if err != nil {
				return gitErr(err)
			}
			if n := strings.TrimSpace(res.Stdout); res.ExitCode != 0 || n != "0" {
				return Refused("%s holds commits that are not on %s (%s); merge or drop them before moving %s back to planned", branch, base, n, id)
			}
			return nil
		}, after: remove}, nil
	}
	return milestoneBranchSteps{}, nil
}

// storedMilestoneStatus is the stored status of id, "" when the store has none.
func storedMilestoneStatus(st ms.Store, id string) (string, error) {
	list, err := st.List()
	if err != nil {
		return "", err
	}
	for _, e := range list {
		if e.ID == id {
			return e.Status, nil
		}
	}
	return "", nil
}

// untagMilestone drops id from the milestone field of every open item that
// carries it, leaving any other milestone on the item in place.
func untagMilestone(c *Ctx, id string) error {
	root, err := backlogScope(c)
	if err != nil {
		return err
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return err
	}
	rows, _, _, err := backlog.OpenRows(be)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var keep []string
		hit := false
		for _, m := range ms.IDs(r.Fields.Get("milestone")) {
			if m == id {
				hit = true
				continue
			}
			keep = append(keep, m)
		}
		if !hit {
			continue
		}
		if _, err := be.SetField(r.ID, "milestone", strings.Join(keep, ", ")); err != nil {
			return err
		}
	}
	return nil
}
