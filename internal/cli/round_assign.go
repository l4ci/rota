package cli

import (
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/pystr"
	"strings"

	"github.com/l4ci/rota/internal/harness"
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
		d.Set("scopes", strs(o.Scopes))
		if o.Inferred {
			d.Set("inferred", true)
		}
		out = append(out, d)
	}
	return out
}

// overlapText is one overlap as text: the holder, then the declared scopes
// the two share ("overlaps #12 (ben): scopes post /items").
func overlapText(o round.Overlap) string {
	w := "overlaps #" + strings.TrimPrefix(o.With, "#")
	if o.Slot != "" {
		w += " (" + o.Slot + ")"
	}
	if len(o.Scopes) > 0 {
		label := ": scopes "
		if o.Inferred {
			label = ": inferred scopes "
		}
		w += label + strings.Join(o.Scopes, ", ")
	}
	return w
}

func roundAssign(fs *flag.FlagSet) RunFunc {
	agent := fs.String("agent", "", "roster name to assign to (default: the first idle slot)")
	body := fs.String("body-file", "", "decisions already settled, passed verbatim to the worker (- for stdin)")
	siblings := fs.String("siblings", "", "issues running alongside, comma-separated")
	checkOnly := fs.Bool("check-only", false, "run the readiness checks and write nothing")
	tier := fs.String("tier", "", "worker tier: light, standard or heavy (default round.tier)")
	tierReason := fs.String("tier-reason", "", "one line on why; required above the default tier")
	kind := fs.String("kind", "", "harness kind: "+harness.KindList()+" (default the issue's harness: label, then the slot's, else claude)")
	model := fs.String("model", "", "model id (default the issue's model: label, then the tier map)")
	acceptCodex := fs.Bool("accept-codex-version", false, "deprecated and ignored: rota no longer checks the Codex version")
	accept := fs.Bool("accept-overlap", false, "skip the file and scope overlap checks")
	acceptOpenPR := fs.Bool("accept-open-pr", false, "assign an issue an open PR already resolves, for a deliberate redo")
	acceptBatch := fs.Bool("accept-batch", false, "assign past a full round.batch for this one call")
	pid := fs.Int("holder-pid", 0, "orchestrator pid, when its ancestry cannot be read")
	return func(c *Ctx, args []string) (Result, error) {
		if len(args) != 1 {
			return Result{}, Usage("round assign takes one item ID")
		}
		if *acceptCodex {
			c.Warn("--accept-codex-version is deprecated and does nothing: rota no longer checks the Codex version")
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := roundcfg.Load(root)
		if err != nil {
			return Result{}, Usage("%s", err.Error())
		}
		ctx, stop := workerContext()
		defer stop()
		var decisions string
		if *body != "" {
			if decisions, err = readBodyText(c, "body-file", *body, ExitUsage); err != nil {
				return Result{}, err
			}
		}
		raw, err := openBacklog(c, root, false, "")
		if err != nil {
			return backlogFail(err)
		}
		be, ok := round.BoardOf(raw)
		if !ok {
			return Result{}, &Error{Exit: ExitInternal, Message: "the backlog backend has no workflow"}
		}
		id, typ, err := resolveItem(be, args[0])
		if err != nil {
			return backlogFail(err)
		}
		env := accountedRoundEnv(c, ctx, root)
		res, err := env.Assign(ctx, root, be, round.AssignOpts{
			ID: id, Agent: *agent, Decisions: decisions, Siblings: pystr.SplitCSV(*siblings),
			CheckOnly: *checkOnly, AcceptOverlap: *accept, AcceptOpenPR: *acceptOpenPR, AcceptBatch: *acceptBatch, HolderPID: *pid,
			Tier: *tier, TierReason: *tierReason, Kind: *kind, Model: *model,
			Settings: set,
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
			if blk.By == round.BlockBatch {
				f.Set("batch", blk.Batch)
				f.Set("open", blk.Open)
				return Result{Data: f}, Refused("%s", blk.Msg).WithHint("land or park an item first, or pass --accept-batch for this one")
			}
			return Result{Data: f}, &Error{Exit: ExitRefused, Message: blk.Msg}
		case err != nil:
			_, ferr := backlogFail(err)
			f := jsonx.NewObject()
			f.Set("changed", res.Changed)
			return Result{Data: f}, ferr
		}
		for _, o := range res.Overlaps {
			c.Warn("%s", overlapText(o))
		}
		d.Set("agent", res.Agent)
		d.Set("branch", res.Branch)
		d.Set("ready", res.Ready())
		d.Set("checks", checkList(res.Checks))
		if len(res.Overlaps) > 0 {
			d.Set("overlaps", overlapList(res.Overlaps))
		}
		setIf(d, "account", res.Account)
		d.Set("kind", res.Kind)
		setIf(d, "kindSource", res.KindSource)
		d.Set("tier", res.Tier)
		setIf(d, "model", res.Model)
		setIf(d, "pick", res.Pick.String())
		setIf(d, "tierReason", res.TierReason)
		if res.SmokeSection != 0 {
			d.Set("smokeSection", res.SmokeSection)
		}
		if res.Host != "" { // solo: the brief comes back instead of going to a pane
			d.Set("host", res.Host.String())
			d.Set("brief", res.Brief)
			d.Set("worktree", res.Worktree)
		}
		d.Set("dispatched", res.Dispatched)
		d.Set("changed", res.Changed)
		if len(res.BestOf) > 0 {
			d.Set("bestOf", bestOfAttempts(res.BestOf))
		}
		if *checkOnly {
			d.Delete("changed")
			text := fmt.Sprintf("%s\t%s\tready=%v", id, res.Agent, res.Ready())
			if len(res.BestOf) > 0 {
				text = fmt.Sprintf("%s\t%s\tready=%v", id, bestOfText(res.BestOf), res.Ready())
			}
			if !res.Ready() {
				d.Set("changed", false)
				return Result{Data: d, Text: text}, Failed("%s is not ready: %s", id, strings.TrimSpace(readyDetail(res)))
			}
			d.Set("changed", false)
			return Result{Data: d, Text: text}, nil
		}
		if len(res.BestOf) > 0 {
			return Result{Data: d, Text: fmt.Sprintf("assigned %s best-of:2 to %s", id, bestOfText(res.BestOf))}, nil
		}
		return Result{Data: d, Text: fmt.Sprintf("assigned %s to %s on %s", id, res.Agent, res.Branch)}, nil
	}
}

// bestOfAttempts is the `bestOf` list of the JSON: one object per attempt.
func bestOfAttempts(as []round.Assigned) []any {
	out := make([]any, 0, len(as))
	for _, a := range as {
		o := jsonx.NewObject()
		o.Set("agent", a.Agent)
		o.Set("branch", a.Branch)
		o.Set("kind", a.Kind)
		o.Set("tier", a.Tier)
		setIf(o, "model", a.Model)
		if a.SmokeSection != 0 {
			o.Set("smokeSection", a.SmokeSection)
		}
		o.Set("dispatched", a.Dispatched)
		out = append(out, o)
	}
	return out
}

// bestOfText names each attempt: slot, kind, branch and smoke section.
func bestOfText(as []round.Assigned) string {
	var out []string
	for _, a := range as {
		s := fmt.Sprintf("%s (%s) on %s", a.Agent, a.Kind, a.Branch)
		if a.SmokeSection != 0 {
			s += fmt.Sprintf(", smoke %d", a.SmokeSection)
		}
		out = append(out, s)
	}
	return strings.Join(out, " and ")
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
