package cli

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
)

// `rota round architecture` (#53): where the round stands against its next
// architecture review and, when one is due, mint and assign it. The counter
// and triggers live in internal/round/architecture.go.

func architectureData(a round.Architecture) *jsonx.Object {
	d := jsonx.NewObject()
	d.Set("every", a.Every)
	d.Set("count", a.Count)
	d.Set("until", a.Until)
	d.Set("due", a.Due)
	setIf(d, "trigger", a.Trigger)
	setIf(d, "since", a.Since)
	setIf(d, "unavailable", a.Unavailable)
	d.Set("idle", a.Idle)
	d.Set("pending", strs(a.Pending))
	d.Set("areas", strs(a.Areas))
	return d
}

// architectureFor reads the counter against the round's current candidates.
func architectureFor(c *Ctx, root string, set roundcfg.Settings) (round.Architecture, error) {
	ctx := c.Context()
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		return round.Architecture{}, err
	}
	recScope, slate := round.SlateOf(root)
	sc := set.Scope
	if recScope != "" {
		sc = recScope
	}
	env := c.deps().RoundEnv(ctx, root)
	cands, err := env.Candidates(ctx, root, be, round.CandidateOpts{Scope: sc, Slate: slate, Shared: set.SharedPaths, ScopeOverlap: set.ScopeOverlap})
	if err != nil {
		return round.Architecture{}, err
	}
	return env.Architecture(ctx, root, be, set, cands)
}

func roundArchitecture(fs *flag.FlagSet) RunFunc {
	check := fs.Bool("check", false, "report the counter and whether a review is due; mint nothing")
	pid := fs.Int("holder-pid", 0, "orchestrator pid, when its ancestry cannot be read")
	return func(c *Ctx, args []string) (Result, error) {
		if err := noArgs(args); err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := roundcfg.Load(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		a, err := architectureFor(c, root, set)
		if err != nil {
			return backlogFail(err)
		}
		d := architectureData(a)
		line := a.Line()
		if line == "" {
			line = "architecture review is off (round.architectureEvery is 0)"
		}
		if *check || !a.Due {
			d.Set("minted", []any{})
			d.Set("changed", false)
			return Result{Data: d, Text: line}, nil
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
		env := c.deps().RoundEnv(ctx, root)
		env.Worker = workerEnvCtx(c, ctx)
		env.Accounts = c.deps().WorkerAccounts()
		ids, err := env.MintReview(ctx, root, be, a, round.Current(root))
		if err != nil {
			_, ferr := backlogFail(err)
			d.Set("minted", strs(ids))
			d.Set("changed", len(ids) > 0)
			return Result{Data: d}, ferr
		}
		d.Set("minted", strs(ids))
		d.Set("changed", true)

		// One review item per idle slot; the rest stay candidates.
		var assigned []any
		var left []string
		lines := []string{fmt.Sprintf("minted %s (%s)", strings.Join(ids, ", "), a.Trigger)}
		for i, id := range ids {
			if i >= a.Idle {
				left = append(left, id)
				continue
			}
			res, err := env.Assign(ctx, root, be, round.AssignOpts{
				ID: id, HolderPID: *pid, Settings: set,
			})
			var blk *round.BlockedError
			if errors.As(err, &blk) {
				left = append(left, id)
				c.Warn("%s not assigned: %s", id, blk.Msg)
				continue
			}
			if err != nil {
				return backlogFail(err)
			}
			o := jsonx.NewObject()
			o.Set("id", id)
			o.Set("agent", res.Agent)
			o.Set("branch", res.Branch)
			if res.Host != "" { // solo: the brief goes to a subagent the orchestrator launches
				o.Set("brief", res.Brief)
				o.Set("worktree", res.Worktree)
			}
			assigned = append(assigned, o)
			lines = append(lines, fmt.Sprintf("assigned %s to %s on %s", id, res.Agent, res.Branch))
		}
		d.Set("assigned", assigned)
		d.Set("unassigned", strs(left))
		return Result{Data: d, Text: strings.Join(lines, "\n")}, nil
	}
}
