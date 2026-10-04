package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundlease"
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
	le := watchEnv()
	cd, err := roundlease.CommonDir(root)
	if err != nil {
		return roundtick.Result{}, Resolution("%v", err)
	}
	lease, st, err := le.Read(cd)
	if err != nil {
		return roundtick.Result{}, err
	}
	if (st != roundlease.Live && st != roundlease.Foreign) || !le.Discover(pid, os.Getenv).SameAs(lease, le.Host) {
		return roundtick.Result{}, errAutopilotStopped
	}
	policy, err := mergePolicy(c)
	if err != nil {
		return roundtick.Result{}, err
	}
	ctx := c.Context()
	renv := withBoard(c, root, roundEnv(ctx, root))
	renv.Worker = workerEnvCtx(ctx)
	renv.Accounts = workerAccounts()

	e := roundtick.Env{Cap: set.AutopilotCap, HumanMerge: policy.Mode != gate.MergeNone}
	if e.Cap == 0 {
		e.Cap = roundtick.DefaultCap
	}
	e.Slots = func() []roundtick.Slot {
		var out []roundtick.Slot
		for _, s := range worker.LoadRegistry(root).Slots() {
			out = append(out, roundtick.Slot{
				Name: worker.Str(s, "name"), State: strings.ToLower(worker.Str(s, "state")),
				Issue: round.SlotIssue(worker.Str(s, "task"), worker.Str(s, "branch"), worker.Str(s, "name")),
				PR:    worker.Str(s, "pr"),
			})
		}
		return out
	}
	e.Queued = func() []string {
		var out []string
		for _, q := range worker.LoadRegistry(root).PRs() {
			if pr := worker.Str(q, "pr"); pr != "" {
				out = append(out, pr)
			}
		}
		return out
	}
	e.Reconcile = func(ctx context.Context) ([]string, []string, error) {
		out, err := renv.Reconcile(ctx, root, true)
		if err != nil {
			return nil, nil, fromWorker(err)
		}
		var repaired, drift []string
		for _, f := range out.Repaired {
			repaired = append(repaired, f.Kind+" "+firstOf(f.Slot, "#"+f.Issue))
		}
		for _, f := range out.Drift {
			drift = append(drift, f.Kind+" "+firstOf(f.Slot, "#"+f.Issue))
		}
		return repaired, drift, nil
	}
	e.Merge = func(ctx context.Context, targets []string) ([]roundtick.Merged, error) {
		return tickMerge(c, root, renv.Base, baseOverride, targets)
	}
	be, err := a4Open(c, root, false, "")
	if err != nil {
		_, ferr := a4Fail(err)
		return roundtick.Result{}, ferr
	}
	board, ok := be.(round.Board)
	if !ok {
		return roundtick.Result{}, &Error{Exit: ExitInternal, Message: "the backlog backend has no workflow"}
	}
	e.Candidates = func(ctx context.Context) ([]roundtick.Candidate, error) {
		scope, slate := round.SlateOf(root)
		if scope == "" {
			scope = set.Scope
		}
		cs, err := renv.Candidates(ctx, root, be, round.CandidateOpts{Scope: scope, Slate: slate, Shared: set.SharedPaths})
		if err != nil {
			return nil, err
		}
		var out []roundtick.Candidate
		for _, cd := range cs {
			out = append(out, roundtick.Candidate{ID: cd.ID, Ready: cd.Ready() && len(cd.Overlaps) == 0})
		}
		return out, nil
	}
	// Assign takes no tier, no kind and never accepts overlap: the round's
	// defaults, and only a candidate that is ready as it stands.
	e.Assign = func(ctx context.Context, id string) (string, error) {
		res, err := renv.Assign(ctx, root, board, round.AssignOpts{ID: id, HolderPID: pid, Settings: set, Getenv: os.Getenv})
		for _, w := range res.Warnings {
			c.Warn("%s", w)
		}
		var blk *round.BlockedError
		if errors.As(err, &blk) && blk.By != round.BlockNoRound {
			return "", &roundtick.Refusal{Why: blk.Msg}
		}
		if err != nil {
			return "", fromWorker(err)
		}
		return res.Agent, nil
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

// tickMerge gates the targets, grouped by the base each merges into: one is a
// `worker gate`, several a `worker train`. A target that did not land is
// reported with its verdict; whether it is held for a person is the verdict's
// call: a stale or provenance bounce goes back to the worker and returns, any
// other failure waits for a person.
func tickMerge(c *Ctx, root, defBase, override string, targets []string) ([]roundtick.Merged, error) {
	var order []string
	groups := map[string][]string{}
	for _, t := range targets {
		b := override
		if b == "" {
			b = targetBase(root, t, defBase)
		}
		if _, ok := groups[b]; !ok {
			order = append(order, b)
		}
		groups[b] = append(groups[b], t)
	}
	var out []roundtick.Merged
	for _, b := range order {
		ts := groups[b]
		if len(ts) == 1 {
			out = append(out, mergeOne(c, ts[0], b))
		} else {
			out = append(out, mergeTrain(c, ts, b)...)
		}
	}
	return out, nil
}

func targetBase(root, target, def string) string {
	s, _, err := worker.LoadRegistry(root).GateTarget(target)
	if err != nil {
		return def
	}
	if b := worker.Str(s, "base"); b != "" {
		return b
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

func holdable(verdict string) bool {
	return verdict != worker.GateStale && verdict != worker.GateProvenanceFail
}

func mergeOne(c *Ctx, target, base string) roundtick.Merged {
	r, err := callVerb(c, workerGate, "--base", base, target)
	if err == nil {
		return roundtick.Merged{Target: target, Landed: true, Detail: "into " + base}
	}
	v := verdictOf(r)
	if v == "" {
		v = "gate-error"
	}
	return roundtick.Merged{Target: target, Verdict: v, Hold: holdable(v), Detail: firstLine(err.Error())}
}

func mergeTrain(c *Ctx, targets []string, base string) []roundtick.Merged {
	args := append([]string{"--base", base}, targets...)
	r, err := callVerb(c, workerTrain, args...)
	if err == nil {
		out := make([]roundtick.Merged, 0, len(targets))
		for _, t := range targets {
			out = append(out, roundtick.Merged{Target: t, Landed: true, Detail: "train into " + base})
		}
		return out
	}
	v := verdictOf(r)
	if v == "" {
		v = "train-error"
	}
	d, _ := r.Data.(*jsonx.Object)
	landed := map[string]bool{}
	culprit := ""
	if d != nil {
		if raw, ok := d.Get("members"); ok {
			list, _ := raw.([]any)
			for _, m := range list {
				if o, ok := m.(*jsonx.Object); ok {
					t, _ := o.Get("target")
					l, _ := o.Get("landed")
					if ok, _ := l.(bool); ok {
						landed[fmt.Sprint(t)] = true
					}
				}
			}
		}
		if cv, ok := d.Get("culprit"); ok {
			culprit, _ = cv.(string)
		}
	}
	out := make([]roundtick.Merged, 0, len(targets))
	for _, t := range targets {
		m := roundtick.Merged{Target: t}
		switch {
		case landed[t]:
			m.Landed, m.Detail = true, "train into "+base
		case v == "base-moved":
			m.Skip = true // nothing landed; the base moved under the train
		case culprit != "" && t != culprit && !strings.Contains(culprit, t):
			m.Skip = true // another member broke the train: retried without it
		default:
			m.Verdict, m.Hold, m.Detail = v, holdable(v), firstLine(err.Error())
		}
		out = append(out, m)
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
