package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/worker"
)

// The round autopilot (#93): `rota round tick` does one pass, `rota round watch
// --autopilot` does one on every wake. The steps and the rules are in
// internal/roundtick; this file wires them to the real verbs.

// errAutopilotStopped says the autopilot has no round to work for: the lease is
// gone (wind-down released it, or another orchestrator took it).
var errAutopilotStopped = errors.New("this process holds no round lease: the autopilot stops")

// callVerb runs another verb in-process. Flags go first: the flag package stops
// at the first positional argument.
func callVerb(c *Ctx, verb func(*flag.FlagSet) RunFunc, args ...string) (Result, error) {
	fs := newFlagSet(c.Path)
	run := verb(fs)
	if err := fs.Parse(args); err != nil {
		return Result{}, Usage("%v", err)
	}
	return run(c, fs.Args())
}

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
	lease, _, held, err := le.Holds(cd, pid, os.Getenv)
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
	renv := withBoard(c, root, c.deps().RoundEnv(ctx, root))
	renv.Worker = workerEnvCtx(c, ctx)
	renv.Accounts = c.deps().WorkerAccounts()

	e := roundtick.Env{Cap: set.AutopilotCap, HumanMerge: policy.Mode != gate.MergeNone}
	if e.Cap == 0 {
		e.Cap = roundtick.DefaultCap
	}
	e.Slots = func() []roundtick.Slot {
		var out []roundtick.Slot
		for _, s := range worker.LoadRegistry(root).Slots() {
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
		for _, q := range worker.LoadRegistry(root).PRs() {
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
		return gateTarget(c, target, base)
	}
	e.Train = func(_ context.Context, targets []string, base string) roundtick.TrainOutcome {
		return trainTargets(c, targets, base)
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
		scope, slate := round.SlateOf(root)
		if scope == "" {
			scope = set.Scope
		}
		cs, err := renv.Candidates(ctx, root, be, round.CandidateOpts{Scope: scope, Slate: slate, Shared: set.SharedPaths, ScopeOverlap: set.ScopeOverlap})
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
		if len(res.BestOf) == 2 { // a best-of:2 issue took two slots
			return []string{res.BestOf[0].Agent, res.BestOf[1].Agent}, nil
		}
		return []string{res.Agent}, nil
	}
	e.ReviewLoop = func(_ context.Context, sl roundtick.Slot) roundtick.ReviewOutcome {
		return reviewStep(c, root, set, sl.Name)
	}
	e.Audit = func(a roundtick.Action) {
		if err := gate.Autopilot(root, "round tick "+a.Action, a.Target, a.Detail); err != nil {
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

func targetBase(root, target, def string) string {
	t, err := worker.LoadRegistry(root).GateTarget(target)
	if err != nil {
		return def
	}
	if t.Base != "" {
		return t.Base
	}
	return def
}

// verdictOf reads the verdict a gate or train result carries.
func verdictOf(r Result) string {
	if d, ok := r.Data.(*jsonx.Object); ok {
		if v, ok := d.Get("verdict"); ok {
			s, _ := v.(string)
			return s
		}
	}
	return ""
}

func gateTarget(c *Ctx, target, base string) roundtick.GateOutcome {
	r, err := callVerb(c, workerGate, "--base", base, target)
	if err == nil {
		return roundtick.GateOutcome{Landed: true}
	}
	return roundtick.GateOutcome{Verdict: verdictOf(r), Detail: firstLine(err.Error())}
}

func trainTargets(c *Ctx, targets []string, base string) roundtick.TrainOutcome {
	args := append([]string{"--base", base}, targets...)
	r, err := callVerb(c, workerTrain, args...)
	if err == nil {
		return roundtick.TrainOutcome{Done: true}
	}
	out := roundtick.TrainOutcome{Verdict: verdictOf(r), Detail: firstLine(err.Error())}
	if d, _ := r.Data.(*jsonx.Object); d != nil {
		if raw, ok := d.Get("members"); ok {
			list, _ := raw.([]any)
			for _, m := range list {
				if o, ok := m.(*jsonx.Object); ok {
					t, _ := o.Get("target")
					l, _ := o.Get("landed")
					if ok, _ := l.(bool); ok {
						out.Landed = append(out.Landed, fmt.Sprint(t))
					}
				}
			}
		}
		if cv, ok := d.Get("culprit"); ok {
			out.Culprit, _ = cv.(string)
		}
	}
	return out
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
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
	if len(lines) == 0 {
		return "nothing to do"
	}
	return strings.Join(lines, "\n")
}

// reviewStep is one pass of the review loop over a done slot with a PR: it
// looks for review input and, under round.reviewLoop auto, relays it to the
// worker. Under manual the input is only reported and the merge is not held:
// the orchestrator runs review-relay, the gate runs as before. Under auto a
// relay that cannot go out (the item is at the bounce cap, the host refused) or
// a poll that failed holds the slot, so the autopilot never merges over review
// input it could not hand back. A failed poll is a warning either way, never a
// silent pass and never a tick item.
func reviewStep(c *Ctx, root string, set roundcfg.Settings, slot string) roundtick.ReviewOutcome {
	ctx := c.Context()
	auto := set.ReviewLoop == roundcfg.ReviewLoopAuto
	pr := ""
	if s := worker.LoadRegistry(root).Slot(slot); s != nil {
		pr = s.PR()
	}
	// The detail stays the same from pass to pass, or the watch would wake on it
	// every time; the error text goes to stderr.
	pollFailed := func(err error) roundtick.ReviewOutcome {
		c.Warn("review poll of %s: %v", slot, err)
		return roundtick.ReviewOutcome{Hold: auto}
	}
	failed := func(why string, err error) roundtick.ReviewOutcome {
		c.Warn("review poll of %s: %v", slot, err)
		return roundtick.ReviewOutcome{Pending: true, Hold: auto, Detail: fmt.Sprintf("%s %s: run `rota round review-relay %s` for the error", why, pr, slot)}
	}
	o, err := reviewOpts(c, root, set, slot)
	if err != nil {
		return pollFailed(err)
	}
	if !auto {
		s := worker.LoadRegistry(root).Slot(slot)
		if s == nil {
			return roundtick.ReviewOutcome{}
		}
		if info, err := o.Forge.PRView(ctx, mustPRNumber(s.PR())); err != nil {
			return pollFailed(err)
		} else if !strings.EqualFold(info.State, "OPEN") {
			return roundtick.ReviewOutcome{}
		}
		b, err := worker.PendingReview(ctx, o.Forge, s, o.Verdict)
		if err != nil {
			return pollFailed(err)
		}
		if b.Empty() {
			return roundtick.ReviewOutcome{}
		}
		return roundtick.ReviewOutcome{Pending: true, Detail: reviewSummary(b)}
	}
	env := c.deps().RoundEnv(ctx, root)
	env.Worker = workerEnvCtx(c, ctx)
	res, err := env.ReviewRelay(ctx, root, o)
	switch {
	case err == nil && res.Nothing:
		return roundtick.ReviewOutcome{}
	case err == nil:
		return roundtick.ReviewOutcome{Relayed: true, Detail: fmt.Sprintf("%d item(s), bounce %d of %s %s", res.Items, res.Bounces, maxText(set.MaxBounces), pr)}
	}
	if bd, ok := exitcode.DataOf[worker.BlockData](err); ok && bd.BlockedBy == "maxBounces" {
		return roundtick.ReviewOutcome{Pending: true, Hold: true, Detail: fmt.Sprintf("%d item(s) waiting on %s; the item is at the bounce cap", res.Items, pr)}
	}
	if res.Items == 0 { // the poll itself failed
		return pollFailed(err)
	}
	return failed(fmt.Sprintf("%d item(s) not relayed", res.Items), err)
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
