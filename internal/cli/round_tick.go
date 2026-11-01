package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
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
		set, err := c.deps().roundSettings(root, roundConfigRequired)
		if err != nil {
			return Result{}, err
		}
		if !set.Autopilot {
			return Result{}, Refused("the autopilot runs only under autonomy.level auto").WithHint("rota config set autonomy.level auto")
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
		visible := r
		visible.Holding = r.FillStatus
		text := tickLines(visible)
		if lines := addTrainNudge(c, root, d, nil); len(lines) > 0 {
			if text == "nothing to do" {
				text = ""
			} else {
				text += "\n"
			}
			text += strings.Join(lines, "\n")
		}
		return Result{Data: d, Text: text}, nil
	}
}

// autopilotTick runs one tick for the lease holder. The lease is checked on
// every tick: wind-down or a takeover ends the autopilot cleanly.
func autopilotTick(c *Ctx, root string, set roundcfg.Settings, baseOverride string, pid int) (roundtick.Result, error) {
	c.deps().freshReads()
	cd, err := rotastate.CommonDirVia(c.Context(), c.deps().Git, root)
	if err != nil {
		return roundtick.Result{}, Resolution("%v", err)
	}
	lease, _, held, err := round.HoldsLease(round.LeaseProbe{Env: c.deps().LeaseEnv(), HolderPID: pid, Getenv: c.deps().Getenv}, cd)
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
	renv.HolderPID = pid
	state := roundtick.LoadState(cd, lease.Round)

	be, err := openBacklog(c, root, false, "")
	if err != nil {
		_, ferr := backlogFail(err)
		return roundtick.Result{}, ferr
	}
	board, ok := round.BoardOf(be)
	if !ok {
		return roundtick.Result{}, &Error{Exit: ExitInternal, Message: "the backlog backend has no workflow"}
	}
	w := &tickWorld{c: c, root: root, set: set, renv: renv, be: be, board: board, state: state}
	e := roundtick.NewEnv(w, roundtick.Config{Settings: set, MergeApproval: policy.Mode, Base: renv.Base, BaseOverride: baseOverride})
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

// reviewReadiness says why a done slot still waits for required reviews,
// and whether its identity/state verdict must go straight to the real gate;
// an incomplete probe or a read error cannot confirm readiness.
func reviewReadiness(c *Ctx, root, slot, base string) (string, bool) {
	ctx, stop := workerContext()
	defer stop()
	r, err := workerEnvCtx(c, ctx).Gate(ctx, root, worker.GateOpts{Slot: slot, Base: base, ReviewsOnly: true, ItemBody: lazyItemBody(c, root)})
	if err != nil {
		return "cannot confirm review verdicts at head: " + strutil.FirstLine(err.Error()), false
	}
	switch r.Verdict {
	case worker.GateStale, worker.GatePRMismatch, worker.GateAlreadyMerged, worker.GateMergedRemotely:
		// These describe the branch or PR, not missing review verdicts. The
		// real gate sends stale work back or handles the PR's current state.
		return "", true
	}
	if r.Verdict == worker.GateReviewMissing {
		return roundtick.WaitingForReview(r.Err, strutil.FirstNonEmpty(r.Branch, slot)), false
	}
	if r.Verdict != worker.GateFresh {
		return "cannot confirm review verdicts at head: " + strutil.FirstNonEmpty(strutil.FirstLine(r.Err), r.Verdict), false
	}
	return "", false
}

// quotaCapped is the tick's cap hook: why no slot may fill, "" when one may.
func quotaCapped(renv round.Env, root string, set roundcfg.Settings) func(context.Context) string {
	return func(ctx context.Context) string { return renv.QuotaCap(ctx, root, set).Reason }
}

func tickData(r roundtick.Result) *jsonx.Object {
	d := jsonx.NewObject()
	if len(r.Holding) > 0 {
		d.Set("holding", strList(r.Holding))
	}
	if len(r.FillStatus) > 0 {
		d.Set("fillStatus", strList(r.FillStatus))
	}
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
	if r.BatchFull {
		d.Set("batchFull", true)
	}
	setIf(d, "deferred", r.Deferred)
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
	lines = append(lines, r.Holding...)
	for _, a := range r.Did {
		lines = append(lines, fmt.Sprintf("did\t%s\t%s\t%s", a.Action, a.Target, dash(a.Detail)))
	}
	for _, it := range r.NeedsYou {
		lines = append(lines, fmt.Sprintf("needs-you\t%s\t%s\t%s", it.Kind, it.Target, it.Why))
	}
	if r.Capped != "" {
		lines = append(lines, "capped\t"+r.Capped)
	}
	if r.BatchFull {
		lines = append(lines, "batch-full")
	}
	if r.Deferred != "" {
		lines = append(lines, "deferred\t"+r.Deferred)
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
	// Tolerant: a failed read is a failed poll (reviewOpts reads strictly).
	s := worker.LoadRegistryTolerant(root).Slot(slot)
	pr := ""
	if s != nil {
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
	src.Poll = func(ctx context.Context) (string, error) {
		return roundtick.PendingSummary(ctx, o.Forge, s, o.Verdict, c.deps().Now())
	}
	src.Relay = func(ctx context.Context) (roundtick.RelayResult, error) {
		res, err := reviewEnv(c, ctx, root, o).ReviewRelay(ctx, root, o)
		out := roundtick.RelayResult{Nothing: res.Nothing, Items: res.Items, Bounces: res.Bounces}
		if bd, ok := exitcode.DataOf[worker.BlockData](err); ok && bd.BlockedBy == blockedby.MaxBounces {
			out.AtCap = true
		}
		return out, err
	}
	return roundtick.ReviewStep(ctx, src)
}

// reviewAdapter wires the tick's architecture review to the backlog and the
// round env. The decision is the tick's (round.DecideReview).
type reviewAdapter struct {
	c     *Ctx
	root  string
	set   roundcfg.Settings
	renv  round.Env
	board round.Board
}

func (r reviewAdapter) Facts(ctx context.Context) (round.Architecture, bool, error) {
	cands, err := r.renv.RoundCandidates(ctx, r.root, r.board, r.set, "")
	if err != nil {
		return round.Architecture{}, false, err
	}
	return r.renv.ArchitectureFacts(ctx, r.root, r.board, r.set, cands)
}

func (r reviewAdapter) Mint(ctx context.Context, a round.Architecture) ([]string, error) {
	return r.renv.MintReview(ctx, r.root, r.board, a, round.Current(r.root))
}

// fallbackLine is the warning a tick prints when an assign moved to a later harness.
func fallbackLine(id string, fb *round.Fallback) string {
	return fmt.Sprintf("fallback\t%s: %s -> %s: %s", tracker.RefOf(id), fb.From, fb.To, fb.Why)
}

// tickWorld is the CLI's side of a tick: every seam roundtick.NewEnv wires,
// answered by the real registry, forge and verbs.
type tickWorld struct {
	c     *Ctx
	root  string
	set   roundcfg.Settings
	renv  round.Env
	be    backlog.Backend
	board round.Board
	state roundtick.State
}

func (w *tickWorld) TolerantRegistry() worker.Registry  { return worker.LoadRegistryTolerant(w.root) }
func (w *tickWorld) Registry() (worker.Registry, error) { return worker.LoadRegistry(w.root) }
func (w *tickWorld) Forge(ctx context.Context) (roundtick.PRViewer, error) {
	return w.c.deps().forge(ctx, w.c.deps().Config(w.root), "", w.root)
}

// Waiting reads the queued PRs once, using the same current-head review
// timestamps as the nudge. A missing timestamp leaves the count rule usable.
func (w *tickWorld) Waiting() []roundtick.ReadyPR {
	var out []roundtick.ReadyPR
	for _, p := range waitingPRsWithState(w.c, w.root, w.state) {
		out = append(out, roundtick.ReadyPR{PR: p.PR, Reviewed: p.Reviewed, ReadySince: p.ReadySince, Immediate: p.Immediate})
	}
	return out
}

func (w *tickWorld) Reconcile(ctx context.Context) (round.Outcome, error) {
	return w.renv.Reconcile(ctx, w.root, true)
}

func (w *tickWorld) Root() string { return w.root }

func (w *tickWorld) Landing(ctx context.Context, req postgate.Request) (postgate.Env, postgate.Request, error) {
	if req.Gate != nil {
		o := req.Gate
		return prepareGate(w.c, ctx, w.root, gateRunOpts{Slot: o.Slot, Base: o.Base, NoWait: o.NoWait})
	}
	o := req.Train
	return prepareTrain(w.c, ctx, w.root, trainRunOpts{Targets: o.Targets, Base: o.Base, NoWait: o.NoWait})
}

func (w *tickWorld) ReviewReadiness(slot, base string) (string, bool) {
	return reviewReadiness(w.c, w.root, slot, base)
}

func (w *tickWorld) ReviewLoop(slot string) roundtick.ReviewOutcome {
	return reviewStep(w.c, w.root, w.set, slot)
}

func (w *tickWorld) Capped(ctx context.Context) string {
	return quotaCapped(w.renv, w.root, w.set)(ctx)
}

func (w *tickWorld) OpenItems() int { return round.OpenItems(w.root, w.board) }

func (w *tickWorld) Candidates(ctx context.Context) ([]round.Candidate, error) {
	candidates, err := w.renv.RoundCandidates(ctx, w.root, w.be, w.set, "")
	warnCandidateMigrations(w.c, candidates)
	return candidates, err
}

// Assign takes no tier, no kind and never accepts overlap: the round's
// defaults, and only a candidate that is ready as it stands.
func (w *tickWorld) Assign(ctx context.Context, id string) (round.Assigned, error) {
	res, err := w.renv.Assign(ctx, w.root, w.board, round.AssignOpts{ID: id})
	for _, warn := range res.Warnings {
		w.c.Warn("%s", warn)
	}
	if err != nil {
		return res, err
	}
	for _, o := range res.Overlaps {
		w.c.Warn("%s %s", tracker.RefOf(id), o.String())
	}
	if fb := res.Fallback; fb != nil {
		w.c.Warn("%s", fallbackLine(id, fb))
	}
	return res, nil
}

func (w *tickWorld) Review() roundtick.Reviewer {
	return reviewAdapter{c: w.c, root: w.root, set: w.set, renv: w.renv, board: w.board}
}

func (w *tickWorld) Audit(a roundtick.Action) {
	if err := gate.Autopilot(w.root, w.c.deps().Now(), "round tick "+a.Action, a.Target, a.Detail); err != nil {
		w.c.Warn("audit line not written: %v", err)
	}
}

func (w *tickWorld) Say(line string) { fmt.Fprintln(w.c.Stderr, line) }
func (w *tickWorld) Now() time.Time  { return w.c.deps().Now() }

// Warn retains the CLI warning envelope for lifecycle report failures.
func (w *tickWorld) Warn(message string) { w.c.Warn("%s", message) }
