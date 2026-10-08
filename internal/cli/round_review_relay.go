package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
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
		set, err := roundcfg.Load(root)
		if err != nil {
			return Result{}, &Error{Exit: ExitInternal, Message: err.Error()}
		}
		ctx, stop := workerContext()
		defer stop()
		c.ctx = ctx
		o, err := reviewOpts(c, root, set, slot)
		if err != nil {
			return Result{}, err
		}
		env := c.deps().RoundEnv(ctx, root)
		env.Worker = workerEnvCtx(c, ctx)
		res, err := env.ReviewRelay(ctx, root, o)
		return reviewRelayResult(res, set, err)
	}
}

// reviewOpts wires ReviewRelay to the project's forge, verdict store and
// escalation path.
func reviewOpts(c *Ctx, root string, set roundcfg.Settings, slot string) (round.ReviewOpts, error) {
	ctx := c.Context()
	cfg := config.Load(rotatree.Config(root))
	fg, err := c.deps().forge(ctx, cfg, "", root)
	if err != nil {
		return round.ReviewOpts{}, trackerErr(err)
	}
	o := round.ReviewOpts{Slot: slot, Forge: fg, MaxBounces: set.MaxBounces}
	if s := worker.LoadRegistry(root).Slot(slot); s != nil {
		o.Verdict = failVerdict(root, s.Branch())
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

// failVerdict is the effective review verdict recorded for branch when it is
// FAIL, as relay input; nil when there is none.
func failVerdict(root, branch string) *worker.ReviewVerdict {
	r, ok := verdict.EffectiveReview(verdict.Load(root).Branches[verdict.BranchKey("", branch)])
	if !ok || r.Verdict != verdict.Fail {
		return nil
	}
	at, err := time.Parse("2006-01-02T15:04:05Z", r.RecordedAt)
	if err != nil {
		return nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s FAIL at %s", r.Kind, strutil.ShortSHA(r.Sha))
	if r.Summary != "" {
		sb.WriteString(": " + r.Summary)
	}
	for _, f := range r.Findings {
		fmt.Fprintf(&sb, "\n  - [%s] %s", f.Severity, f.Title)
		if f.File != "" {
			fmt.Fprintf(&sb, " (%s", f.File)
			if f.Line > 0 {
				fmt.Fprintf(&sb, ":%d", f.Line)
			}
			sb.WriteString(")")
		}
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
	return Result{Data: d, Text: fmt.Sprintf("relayed %d item(s) to %s: bounce %d of %s", res.Items, res.Slot, res.Bounces, maxText(set.MaxBounces))}, nil
}
