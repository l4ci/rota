package round

import (
	"context"
	"fmt"
	"slices"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/milestone"
)

// milestoneTarget is the first active milestone the issue is tagged to, "" for
// none. It reads the tags and the milestone store only: no git, so a caller
// that polls (the reconcile pass) can compare before it asks origin anything.
func (e Env) milestoneTarget(root string, be Board, it *backlog.Item) (string, error) {
	tagged := backlog.ParseMilestones(it.Fields.Get("milestone"))
	if len(tagged) == 0 {
		return "", nil
	}
	var store milestone.Store = milestone.FileStore{Root: root}
	if b, ok := be.(interface{ MilestoneStore() milestone.Store }); ok {
		store = b.MilestoneStore()
	}
	list, err := store.List()
	if err != nil {
		return "", err
	}
	active := milestone.ActiveIDs(list)
	for _, id := range tagged {
		if slices.Contains(active, id) {
			return id, nil
		}
	}
	return "", nil
}

// milestoneBase is the branch the issue targets: milestone/<id> for the first
// milestone it is tagged to that is active, "" for an untagged issue, one
// tagged only to a milestone that is not active, or a project whose origin
// holds no milestone branches (no origin remote, so none was ever cut). An
// active milestone whose branch origin lacks keeps the round base, with a
// warning.
func (e Env) milestoneBase(ctx context.Context, root string, be Board, it *backlog.Item) (base, warning string, err error) {
	id, err := e.milestoneTarget(root, be, it)
	if err != nil || id == "" {
		return "", "", err
	}
	if res, err := e.Git(ctx, root, "remote", "get-url", "origin"); err != nil || res.ExitCode != 0 {
		return "", "", nil
	}
	branch := milestone.Branch(id)
	res, err := e.Git(ctx, root, "ls-remote", "--exit-code", "--heads", "origin", "refs/heads/"+branch)
	switch {
	case err != nil || (res.ExitCode != 0 && res.ExitCode != 2):
		return "", "", blocked(BlockFetch, "could not read origin to look for %s (%s)", branch, gitWhy(res.Stderr, err))
	case res.ExitCode == 2:
		// An active milestone from before branches were cut: its issues
		// keep the round base rather than stall.
		return "", fmt.Sprintf("%s is tagged to active milestone %s, but origin has no %s: it keeps the round base (run `rota milestone status %s --to active` to cut it)", it.ID, id, branch, id), nil
	}
	return branch, "", nil
}

// fetchBase fetches origin/<base> and keeps a local <base> beside it (the
// worktree-side verbs that cut from or compare against a slot's base read the
// local branch), then returns the ref a branch is cut from. A local branch
// that cannot fast-forward (checked out elsewhere) stays as it is.
func (e Env) fetchBase(ctx context.Context, root, base string) (string, error) {
	res, err := e.Git(ctx, root, "fetch", "-q", "origin", base)
	if err != nil || res.ExitCode != 0 {
		return "", blocked(BlockFetch, "could not fetch origin/%s to cut the branch from (%s)", base, gitWhy(res.Stderr, err))
	}
	from := "origin/" + base
	if res, err := e.Git(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+base); err == nil && res.ExitCode != 0 {
		if res, err := e.Git(ctx, root, "branch", base, from); err != nil || res.ExitCode != 0 {
			return "", blocked(BlockFetch, "could not create %s from %s (%s)", base, from, gitWhy(res.Stderr, err))
		}
	} else {
		e.Git(ctx, root, "fetch", "-q", "origin", base+":"+base)
	}
	return from, nil
}
