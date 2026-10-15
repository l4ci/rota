package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/tracker"
	"strings"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// The round autopilot (#93): `rota round tick` does one pass, `rota round watch
// --autopilot` does one on every wake. The steps and the rules are in
// internal/roundtick; this file wires them to the real verbs.

// errAutopilotStopped says the autopilot has no round to work for: the lease is
// gone (wind-down released it, or another orchestrator took it).
var errAutopilotStopped = errors.New("this process holds no round lease: the autopilot stops")

func roundTick(fs *flag.FlagSet) RunFunc {
	base := fs.String("base", "", "the cycle branch PRs merge into (default each PR's recorded base)")
	pid := fs.Int("holder-pid", 0, "orchestrator pid for the lease, when its ancestry cannot be read")
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
		if !set.Autopilot {
			return Result{}, Refused("round.autopilot is off").WithHint("rota config set round.autopilot true")
		}
		r, err := autopilotTick(c, root, set, *base, *pid)
		switch {
		case errors.Is(err, errAutopilotStopped):
			d := jsonx.NewObject()
			d.Set("stopped", true)
			d.Set("changed", false)
			return Result{Data: d}, Refused("%v", err).WithHint("rota round start takes the lease again")
		case err != nil:
			return Result{}, err
		}
		d := tickData(r)
		d.Set("changed", len(r.Did) > 0)
		return Result{Data: d, Text: tickLines(r)}, nil
	}
}

// autopilotTick runs one tick for the lease holder. The lease is checked on
// every tick: wind-down or a takeover ends the autopilot cleanly.
func autopilotTick(c *Ctx, root string, set roundcfg.Settings, baseOverride string, pid int) (roundtick.Result, error) {
	c.deps().freshReads()
	le := c.deps().LeaseEnv()
	cd, err := rotastate.CommonDir(root)
	if err != nil {
		return roundtick.Result{}, Resolution("%v", err)
	}
	lease, _, held, err := le.Holds(cd, pid, c.deps().Getenv)
	if err != nil {
		return roundtick.Result{}, err
	}
	if !held {
		return roundtick.Result{}, errAutopilotStopped
	}
	policy, err := mergePolicy(c)
	if err != nil {
		return roundtick.Result{}, err
	}
	ctx := c.Context()
	renv := withBoard(c, root, accountedRoundEnv(c, ctx, root))

	e := roundtick.Env{Cap: set.AutopilotCap, HumanMerge: policy.Mode != gate.MergeNone}
	if e.Cap == 0 {
		e.Cap = roundtick.DefaultCap
	}
	e.Capped = quotaCapped(renv, root, set)
	e.Slots = func() []roundtick.Slot {
		var out []roundtick.Slot
		// Tolerant: a tick callback with no error return; the tick reconciles strictly.
		for _, s := range worker.LoadRegistryTolerant(root).Slots() {
			out = append(out, roundtick.Slot{
				Name: s.Name(), State: strings.ToLower(s.State()),
				Issue: s.HeldID(),
				PR:    s.PR(),
			})
		}
		return out
	}
	e.Queued = func() []string {
		var out []string
		for _, q := range worker.LoadRegistryTolerant(root).PRs() { // tolerant, as Slots above
			if q.PR != "" {
				out = append(out, q.PR)
			}
		}
		return out
	}
	e.Reconcile = func(ctx context.Context) ([]string, []string, error) {
		out, err := renv.Reconcile(ctx, root, true)
		if err != nil {
			return nil, nil, err
		}
		var repaired, drift []string
		for _, f := range out.Repaired {
			repaired = append(repaired, f.Kind+" "+f.Key())
		}
		for _, f := range out.Drift {
			drift = append(drift, f.Kind+" "+f.Key())
		}
		return repaired, drift, nil
	}
	e.BaseOf = func(target string) string {
		if baseOverride != "" {
			return baseOverride
		}
		return targetBase(root, target, renv.Base)
	}
	e.Gate = func(_ context.Context, target, base string) roundtick.GateOutcome {
		return gateTarget(c, root, target, base)
	}
	e.Train = func(_ context.Context, targets []string, base string) roundtick.TrainOutcome {
		return trainTargets(c, root, targets, base)
	}
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		_, ferr := backlogFail(err)
		return roundtick.Result{}, ferr
	}
	board, ok := round.BoardOf(be)
	if !ok {
		return roundtick.Result{}, &Error{Exit: ExitInternal, Message: "the backlog backend has no workflow"}
	}
	e.Candidates = func(ctx context.Context) ([]roundtick.Candidate, error) {
		cs, err := renv.RoundCandidates(ctx, root, be, set, "")
		if err != nil {
			return nil, err
		}
		var out []roundtick.Candidate
		for _, cd := range cs {
			out = append(out, roundtick.Candidate{ID: cd.ID, Ready: cd.Ready()})
		}
		return out, nil
	}
	e.Review = func(ctx context.Context) ([]string, error) {
		a, err := architectureFor(c, root, set)
		if err != nil || !a.Due {
			return nil, err
		}
		return renv.MintReview(ctx, root, board, a, round.Current(root))
	}
	// Assign takes no tier, no kind and never accepts overlap: the round's
	// defaults, and only a candidate that is ready as it stands.
	e.Assign = func(ctx context.Context, id string) ([]string, error) {
		res, err := renv.Assign(ctx, root, board, round.AssignOpts{ID: id, HolderPID: pid, Settings: set})
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		if err != nil {
			return nil, err
		}
		for _, o := range res.Overlaps {
			c.Warn("%s %s", tracker.RefOf(id), overlapText(o))
		}
		if len(res.BestOf) == 2 { // a best-of:2 issue took two slots
			return []string{res.BestOf[0].Agent, res.BestOf[1].Agent}, nil
		}
		return []string{res.Agent}, nil
	}
	e.ReviewLoop = func(_ context.Context, sl roundtick.Slot) roundtick.ReviewOutcome {
		return reviewStep(c, root, set, sl.Name)
	}
	e.Audit = func(a roundtick.Action) {
		if err := gate.Autopilot(root, c.deps().Now(), "round tick "+a.Action, a.Target, a.Detail); err != nil {
			c.Warn("audit line not written: %v", err)
		}
	}
	state := roundtick.LoadState(cd, lease.Round)
	if state.Stopped {
		return roundtick.Result{}, errAutopilotStopped
	}
	state.Apply(&e)
	r, err := roundtick.Run(ctx, e)
	if err != nil {
		return r, err
	}
	if err := roundtick.SaveState(cd, lease.Round, r); err != nil {
		c.Warn("autopilot state not saved: %v", err)
	}
	return r, nil
}

// quotaCapped is the tick's cap hook: why no slot may fill, "" when one may.
func quotaCapped(renv round.Env, root string, set roundcfg.Settings) func(context.Context) string {
	return func(ctx context.Context) string { return renv.QuotaCap(ctx, root, set).Reason }
}

func targetBase(root, target, def string) string {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return def // the gate refuses a corrupt registry itself; this only picks a base for it
	}
	t, err := reg.GateTarget(target)
	if err != nil {
		return def
	}
	if t.Base != "" {
		return t.Base
	}
	return def
}

// gateTarget gates one target for the tick through the same run `worker gate`
// does, bookkeeping included, and reads the verdict off the result.
func gateTarget(c *Ctx, root, target, base string) roundtick.GateOutcome {
	run := runGate(c, root, gateRunOpts{Slot: target, Base: base})
	if run.Err == nil && !run.Refused {
		if run.BookErr != nil {
			fmt.Fprintln(c.Stderr, "BOUNCE-COUNT "+target+" — "+run.BookErr.Error())
		}
		if run.Result.OK() {
			return roundtick.GateOutcome{Landed: true}
		}
		return roundtick.GateOutcome{Verdict: run.Result.Verdict, Detail: strutil.FirstLine(run.Result.Err)}
	}
	return roundtick.GateOutcome{Verdict: failedVerdict(run.Result.Verdict, run.Err, run.Refused), Detail: failureDetail(run.Err, run.Result.Err)}
}

// trainTargets lands several targets for the tick through the same run
// `worker train` does, bookkeeping included.
func trainTargets(c *Ctx, root string, targets []string, base string) roundtick.TrainOutcome {
	run := runTrain(c, root, trainRunOpts{Targets: targets, Base: base})
	r := run.Result
	if run.Err == nil && !run.Refused {
		for _, f := range run.Book {
			fmt.Fprintln(c.Stderr, "BOUNCE-COUNT "+f.Target+" — "+f.Err.Error())
		}
		if r.OK() {
			return roundtick.TrainOutcome{Done: true}
		}
	}
	out := roundtick.TrainOutcome{Verdict: failedVerdict(r.Verdict, run.Err, run.Refused), Detail: failureDetail(run.Err, r.Err)}
	if run.Err != nil && !run.Refused {
		return out // died on an error: nothing of the result is reported
	}
	out.Culprit = r.Culprit
	for _, m := range r.Members {
		if m.Landed {
			out.Landed = append(out.Landed, m.Target)
		}
	}
	return out
}

// failedVerdict is the verdict a failed gate or train reports to the tick: the
// run's own, unless it died with an error that is no refusal.
func failedVerdict(verdict string, err error, refused bool) string {
	if err != nil && !refused {
		return ""
	}
	return verdict
}

// failureDetail is the first line of why a gate or train failed.
func failureDetail(err error, msg string) string {
	if err != nil {
		return strutil.FirstLine(err.Error())
	}
	return strutil.FirstLine(msg)
}

func tickData(r roundtick.Result) *jsonx.Object {
	d := jsonx.NewObject()
	did := make([]any, 0, len(r.Did))
	for _, a := range r.Did {
		o := jsonx.NewObject()
		o.Set("action", a.Action)
		o.Set("target", a.Target)
		setIf(o, "detail", a.Detail)
		did = append(did, o)
	}
	d.Set("did", did)
	setIf(d, "capped", r.Capped)
	items := func(l []roundtick.Item) []any {
		out := make([]any, 0, len(l))
		for _, it := range l {
			o := jsonx.NewObject()
			o.Set("kind", it.Kind)
			o.Set("target", it.Target)
			o.Set("why", it.Why)
			out = append(out, o)
		}
		return out
	}
	d.Set("needsYou", items(r.NeedsYou))
	d.Set("new", items(r.New))
	return d
}

func tickLines(r roundtick.Result) string {
	var lines []string
	for _, a := range r.Did {
		lines = append(lines, fmt.Sprintf("did\t%s\t%s\t%s", a.Action, a.Target, dash(a.Detail)))
	}
	for _, it := range r.NeedsYou {
		lines = append(lines, fmt.Sprintf("needs-you\t%s\t%s\t%s", it.Kind, it.Target, it.Why))
	}
	if r.Capped != "" {
		lines = append(lines, "capped\t"+r.Capped)
	}
	if len(lines) == 0 {
		return "nothing to do"
	}
	return strings.Join(lines, "\n")
}

// reviewStep wires the forge, registry and config into roundtick.ReviewStep,
// which owns the manual/auto decision and the holds.
func reviewStep(c *Ctx, root string, set roundcfg.Settings, slot string) roundtick.ReviewOutcome {
	ctx := c.Context()
	pr := ""
	// Tolerant: a failed read is a failed poll (reviewOpts reads strictly).
	if s := worker.LoadRegistryTolerant(root).Slot(slot); s != nil {
		pr = s.PR()
	}
	o, err := reviewOpts(c, root, set, slot)
	src := roundtick.ReviewSources{
		Slot: slot, PR: pr, Auto: set.ReviewLoop == roundcfg.ReviewLoopAuto,
		MaxBounces: maxText(set.MaxBounces), SetupErr: err, Warn: c.Warn,
	}
	if err != nil {
		return roundtick.ReviewStep(ctx, src)
	}
	var s *worker.Slot
	src.Open = func(ctx context.Context) (bool, error) {
		s = worker.LoadRegistryTolerant(root).Slot(slot) // reviewOpts above read it strictly
		if s == nil {
			return false, nil
		}
		info, err := o.Forge.PRView(ctx, mustPRNumber(s.PR()))
		if err != nil {
			return false, err
		}
		return strings.EqualFold(info.State, "OPEN"), nil
	}
	src.Pending = func(ctx context.Context) (string, error) {
		b, err := worker.PendingReview(ctx, o.Forge, s, o.Verdict, c.deps().Now())
		if err != nil || b.Empty() {
			return "", err
		}
		return reviewSummary(b), nil
	}
	src.Relay = func(ctx context.Context) (roundtick.RelayResult, error) {
		env := c.deps().RoundEnv(ctx, root)
		env.Worker = workerEnvCtx(c, ctx)
		res, err := env.ReviewRelay(ctx, root, o)
		out := roundtick.RelayResult{Nothing: res.Nothing, Items: res.Items, Bounces: res.Bounces}
		if bd, ok := exitcode.DataOf[worker.BlockData](err); ok && bd.BlockedBy == "maxBounces" {
			out.AtCap = true
		}
		return out, err
	}
	return roundtick.ReviewStep(ctx, src)
}

// mustPRNumber is the number of a slot's PR; 0 when it has none.
func mustPRNumber(pr string) int {
	n, _ := worker.PRRefNumber(pr)
	return n
}

// reviewSummary is "<n> from <authors>", the value of a slot's review key.
func reviewSummary(b worker.ReviewBatch) string {
	n := len(b.Items)
	from := strings.Join(b.Authors(), ", ")
	if b.Verdict != "" {
		if n == 0 {
			return "review verdict FAIL " + b.PR
		}
		from += " and a FAIL verdict"
	}
	return fmt.Sprintf("%d from %s %s", n, from, b.PR)
}
