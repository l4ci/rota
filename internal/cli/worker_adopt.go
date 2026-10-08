package cli

import (
	"errors"
	"flag"
	"fmt"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
)

// `rota worker adopt`: register work another tool started as a hostless slot;
// the steps are round.Env.Adopt.
func workerAdopt(fs *flag.FlagSet) RunFunc {
	issue := fs.String("issue", "", "the issue the work resolves")
	name := fs.String("name", "", "slot name (default ext-<n>)")
	pr := fs.String("pr", "", "the work's PR URL, when it has one")
	accept := fs.Bool("accept-overlap", false, "skip the file-overlap check only")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("worker adopt takes one branch or worktree path")
		}
		if *issue == "" {
			return Result{}, Usage("--issue is required")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := roundcfg.Load(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		ctx, stop := workerContext()
		defer stop()
		raw, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		be, ok := round.BoardOf(raw)
		if !ok {
			return Result{}, &Error{Exit: ExitInternal, Message: "the backlog backend has no workflow"}
		}
		id, _, err := resolveItem(be, *issue)
		if err != nil {
			return backlogFail(err)
		}
		env := c.deps().RoundEnv(ctx, root)
		env.Worker = workerEnvCtx(c, ctx)
		res, err := env.Adopt(ctx, root, be, round.AdoptOpts{
			Ref: args[0], Issue: id, Name: *name, PR: *pr, AcceptOverlap: *accept, Shared: set.SharedPaths,
		})
		var blk *round.BlockedError
		var xe *Error
		switch {
		case errors.As(err, &blk):
			f := jsonx.NewObject()
			f.Set("blockedBy", blk.By)
			if blk.Readiness != nil {
				f.Set("checks", checkList(blk.Readiness.Checks))
				if len(blk.Readiness.Overlaps) > 0 {
					f.Set("overlaps", overlapList(blk.Readiness.Overlaps))
				}
			}
			f.Set("changed", false)
			return Result{Data: f}, &Error{Exit: ExitRefused, Message: blk.Msg}
		case errors.As(err, &xe):
			return Result{}, err
		case err != nil:
			return backlogFail(err)
		}
		d := jsonx.NewObject()
		d.Set("slot", res.Slot)
		d.Set("branch", res.Branch)
		setIf(d, "worktree", res.Worktree)
		d.Set("issue", res.Issue)
		setIf(d, "pr", res.PR)
		if len(res.Overlaps) > 0 {
			d.Set("overlaps", overlapList(res.Overlaps))
		}
		d.Set("changed", true)
		return Result{Data: d, Text: fmt.Sprintf("adopted %s %s #%s", res.Slot, res.Branch, res.Issue)}, nil
	}
}
