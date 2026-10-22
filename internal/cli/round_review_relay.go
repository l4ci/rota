package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/stamp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// roundReviewRelay is `rota round review-relay <slot>`: what the orchestrator
// did by hand when a reviewer commented on a finished worker's PR. It relays the
// new review input to the worker as a counted bounce, or escalates at
// round.maxBounces. It never gates or merges.
func roundReviewRelay(fs *flag.FlagSet) RunFunc {
	return func(c *Ctx, args []string) (Result, error) {
		slot, err := oneArg(args, "slot")
		if err != nil {
			return Result{}, err
		}
		root, err := c.Root()
		if err != nil {
			return Result{}, err
		}
		set, err := c.deps().roundSettings(root, roundConfigRequired)
		if err != nil {
			return Result{}, err
		}
		ctx, stop := workerContext()
		defer stop()
		c.ctx = ctx
		o, err := reviewOpts(c, root, set, slot)
		if err != nil {
			return Result{}, err
		}
		res, err := reviewEnv(c, ctx, root, o).ReviewRelay(ctx, root, o)
		return reviewRelayResult(res, set, err)
	}
}

// reviewOpts wires ReviewRelay to the project's forge, verdict store and
// escalation path.
func reviewOpts(c *Ctx, root string, set roundcfg.Settings, slot string) (round.ReviewOpts, error) {
	ctx := c.Context()
	cfg := c.deps().Config(root)
	fg, err := c.deps().forge(ctx, cfg, "", root)
	if err != nil {
		return round.ReviewOpts{}, trackerErr(err)
	}
	o := round.ReviewOpts{Slot: slot, Forge: fg, MaxBounces: set.MaxBounces, FreshAt: set.FreshAt, Settings: set}
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return round.ReviewOpts{}, err
	}
	if s := reg.Slot(slot); s != nil {
		o.Verdict = failVerdict(root, s.Branch())
		o.OpenFindings = openFindings(root, s.Branch())
	}
	if set.FreshAt > 0 {
		// The backlog is only opened when a fresh transfer can happen; one that
		// cannot be opened leaves the relay to resume the worker in place.
		if _, be, err := moveEnv(c, root); err == nil {
			o.Board = be
		} else {
			c.Warn("round.freshAt skipped: %v", err)
		}
	}
	o.Escalate = func(ctx context.Context, number int, slot, title, body string) error {
		env := c.deps().escalationEnv()
		if env.Forge == nil {
			env.Forge = escalationForge(c)
		}
		_, err := escalation.Send(ctx, env, root, escalation.SendOpts{Number: number, PR: true, Slot: slot, Title: title, Body: body})
		return err
	}
	return o, nil
}

// reviewEnv is the env ReviewRelay runs in: the round env with the meter reader
// but no account picking, and the backlog a fresh transfer moves the claim on.
func reviewEnv(c *Ctx, ctx context.Context, root string, o round.ReviewOpts) round.Env {
	env := c.deps().RoundEnv(ctx, root)
	env.Worker = workerEnvCtx(c, ctx)
	if o.Board != nil {
		env.Board = o.Board
	}
	return env
}

// findingLine is one review finding as a relay or brief line.
func findingLine(f verdict.Finding) string {
	line := fmt.Sprintf("[%s] %s", f.Severity, f.Title)
	if f.File != "" {
		line += fmt.Sprintf(" (%s", f.File)
		if f.Line > 0 {
			line += fmt.Sprintf(":%d", f.Line)
		}
		line += ")"
	}
	return line
}

// failRecords are branch's FAIL verdicts a relay carries: the effective review
// and the cross-harness review (#1612), each when it is FAIL.
func failRecords(root, branch string) []verdict.Record {
	list := verdict.Load(root).Branches[verdict.BranchKey("", branch)]
	var out []verdict.Record
	if r, ok := verdict.EffectiveFail(list); ok {
		out = append(out, r)
	}
	if r, ok := verdict.CrossFail(list); ok {
		out = append(out, r)
	}
	return out
}

// openFindings are the findings of branch's FAIL reviews: after a re-review
// those are the NOT ADDRESSED ones plus any the fix introduced
// (skills/rota-review/re-review.md). Nil when there are none.
func openFindings(root, branch string) []string {
	var out []string
	for _, r := range failRecords(root, branch) {
		for _, f := range r.Findings {
			out = append(out, findingLine(f))
		}
	}
	return out
}

// failVerdict is branch's FAIL reviews as relay input, stamped with the newest
// one's time; nil when there is none.
func failVerdict(root, branch string) *worker.ReviewVerdict {
	var sb strings.Builder
	var at time.Time
	for i, r := range failRecords(root, branch) {
		t, err := stamp.Parse(r.RecordedAt)
		if err != nil {
			return nil
		}
		if t.After(at) {
			at = t
		}
		if i > 0 {
			sb.WriteString("\n")
		}
		fmt.Fprintf(&sb, "%s FAIL at %s", r.Kind, strutil.ShortSHA(r.Sha))
		if r.Harness != "" {
			fmt.Fprintf(&sb, " (%s)", strings.TrimSpace(r.Harness+" "+r.Model))
		}
		if r.Summary != "" {
			sb.WriteString(": " + r.Summary)
		}
		for _, f := range r.Findings {
			sb.WriteString("\n  - " + findingLine(f))
		}
	}
	if sb.Len() == 0 {
		return nil
	}
	return &worker.ReviewVerdict{At: at, Text: sb.String()}
}

func reviewRelayResult(res round.Relayed, set roundcfg.Settings, err error) (Result, error) {
	d := jsonx.NewObject()
	d.Set("slot", res.Slot)
	setIf(d, "pr", res.PR)
	d.Set("items", res.Items)
	d.Set("bounces", res.Bounces)
	d.Set("max", set.MaxBounces)
	setIf(d, "fresh", res.Fresh)
	setIf(d, "freshSkipped", res.FreshSkipped)
	if err != nil {
		d.Set("escalated", res.Escalated)
		d.Set("changed", res.Escalated)
		var we *exitcode.Error
		if errors.As(err, &we) {
			if bd, ok := exitcode.DataOf[worker.BlockData](err); ok {
				d.Set("blockedBy", bd.BlockedBy)
				return Result{Data: d}, err
			}
		}
		return Result{}, err
	}
	if res.Nothing {
		d.Set("changed", false)
		return Result{Data: d, Text: "nothing to relay"}, nil
	}
	d.Set("changed", true)
	if res.Fresh != "" {
		return Result{Data: d, Text: fmt.Sprintf("transferred to %s with a fresh context: bounce %d of %s (round.freshAt)", res.Fresh, res.Bounces, maxText(set.MaxBounces))}, nil
	}
	text := fmt.Sprintf("relayed %d item(s) to %s: bounce %d of %s", res.Items, res.Slot, res.Bounces, maxText(set.MaxBounces))
	if res.FreshSkipped != "" {
		text += fmt.Sprintf("\nround.freshAt: not transferred, relayed in place: %s", res.FreshSkipped)
	}
	return Result{Data: d, Text: text}, nil
}
