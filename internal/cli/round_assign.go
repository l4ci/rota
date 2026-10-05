package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
)

// The C3 verb `rota round assign`; the steps are round.Env.Assign.

func overlapList(os []round.Overlap) []any {
	out := make([]any, 0, len(os))
	for _, o := range os {
		d := jsonx.NewObject()
		d.Set("with", o.With)
		d.Set("slot", o.Slot)
		d.Set("paths", strs(o.Paths))
		out = append(out, d)
	}
	return out
}

func roundAssign(fs *flag.FlagSet) RunFunc {
	agent := fs.String("agent", "", "roster name to assign to (default: the first idle slot)")
	body := fs.String("body-file", "", "decisions already settled, passed verbatim to the worker (- for stdin)")
	siblings := fs.String("siblings", "", "issues running alongside, comma-separated")
	checkOnly := fs.Bool("check-only", false, "run the readiness checks and write nothing")
	tier := fs.String("tier", "", "worker tier: light, standard or heavy (default round.tier)")
	tierReason := fs.String("tier-reason", "", "one line on why; required above the default tier")
	kind := fs.String("kind", "", "harness kind: claude or codex (default the slot's, else claude)")
	acceptCodex := fs.Bool("accept-codex-version", false, "let this call through a Codex CLI outside the supported range")
	accept := fs.Bool("accept-overlap", false, "skip the file-overlap check only")
	acceptOpenPR := fs.Bool("accept-open-pr", false, "assign an issue an open PR already resolves, for a deliberate redo")
	pid := fs.Int("holder-pid", 0, "orchestrator pid, when its ancestry cannot be read")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round assign takes one item ID")
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
		bf := *body
		if bf == "-" {
			f, err := os.CreateTemp("", "rota-round-decisions-")
			if err != nil {
				return Result{}, err
			}
			defer os.Remove(f.Name())
			if _, err := f.ReadFrom(c.Stdin); err != nil {
				return Result{}, err
			}
			f.Close()
			bf = f.Name()
		}
		raw, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		be, ok := raw.(round.Board)
		if !ok {
			return Result{}, &Error{Exit: ExitInternal, Message: "the backlog backend has no workflow"}
		}
		id, typ, err := resolveItem(be, args[0])
		if err != nil {
			return backlogFail(err)
		}
		env := roundEnv(ctx, root)
		env.Worker = workerEnvCtx(ctx)
		env.Accounts = workerAccounts()
		res, err := env.Assign(ctx, root, be, round.AssignOpts{
			ID: id, Agent: *agent, BodyFile: bf, Siblings: splitList(*siblings),
			CheckOnly: *checkOnly, AcceptOverlap: *accept, AcceptOpenPR: *acceptOpenPR, HolderPID: *pid,
			Tier: *tier, TierReason: *tierReason, Kind: *kind, AcceptCodexVersion: *acceptCodex,
			Settings: set, Getenv: os.Getenv,
		})
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		d := jsonx.NewObject()
		d.Set("id", id)
		d.Set("type", typ)
		var blk *round.BlockedError
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
		case err != nil:
			_, ferr := backlogFail(err)
			f := jsonx.NewObject()
			f.Set("changed", res.Changed)
			return Result{Data: f}, ferr
		}
		d.Set("agent", res.Agent)
		d.Set("branch", res.Branch)
		d.Set("ready", res.Ready())
		d.Set("checks", checkList(res.Checks))
		setIf(d, "account", res.Account)
		d.Set("kind", res.Kind)
		d.Set("tier", res.Tier)
		setIf(d, "model", res.Model)
		setIf(d, "tierReason", res.TierReason)
		if res.Host != "" { // solo: the brief comes back instead of going to a pane
			d.Set("host", res.Host)
			d.Set("brief", res.Brief)
			d.Set("worktree", res.Worktree)
		}
		d.Set("dispatched", res.Dispatched)
		d.Set("changed", res.Changed)
		if *checkOnly {
			d.Delete("changed")
			text := fmt.Sprintf("%s\t%s\tready=%v", id, res.Agent, res.Ready())
			if !res.Ready() {
				d.Set("changed", false)
				return Result{Data: d, Text: text}, Failed("%s is not ready: %s", id, strings.TrimSpace(readyDetail(res)))
			}
			d.Set("changed", false)
			return Result{Data: d, Text: text}, nil
		}
		return Result{Data: d, Text: fmt.Sprintf("assigned %s to %s on %s", id, res.Agent, res.Branch)}, nil
	}
}

func readyDetail(r round.Assigned) string {
	var out []string
	for _, ch := range r.Checks {
		if !ch.OK {
			out = append(out, ch.Name+": "+strings.Join(ch.Detail, "; "))
		}
	}
	return strings.Join(out, " | ")
}
