package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/l4ci/rota/internal/repos"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// The round autopilot (#93): `rota round tick` does one pass, `rota round watch
// --autopilot` does one on every wake. The steps and the rules are in
// internal/roundtick; this file wires them to the real verbs.

func roundTick(fs *flag.FlagSet) RunFunc {
	observe := fs.Bool("observe", false, "report proposed autopilot actions without executing them")
	base := fs.String("base", "", "the cycle branch PRs merge into (default each PR's recorded base)")
	return holderPIDFlag(fs, "orchestrator pid for the lease, when its ancestry cannot be read", func(c *Ctx, args []string) (Result, error) {
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
		if !set.Autopilot && !*observe {
			return Result{}, Refused("the autopilot runs only under autonomy.level auto").WithHint("rota config set autonomy.level auto")
		}
		r, err := runAutopilotTick(c, root, set, *base, c.holderPID, *observe)
		switch {
		case roundtick.IsStopped(err):
			d := jsonx.NewObject()
			d.Set("stopped", true)
			d.Set("changed", false)
			if errors.Is(err, roundtick.ErrBaseRed) {
				d.Set("autopilotStopped", true)
				return Result{Data: d}, Refused("%v", err).WithHint("rota round wind-down")
			}
			if errors.Is(err, roundtick.ErrStopped) {
				return Result{Data: d}, Refused("%v", err)
			}
			return Result{Data: d}, Refused("%v", err).WithHint("rota round start takes the lease again")
		case err != nil:
			return Result{}, err
		}
		d := tickData(r)
		d.Set("changed", len(r.Did) > 0)
		visible := r
		visible.Holding = r.FillStatus
		text := tickLines(visible)
		if lines := tickNudge(c, root, d, r.Observed); len(lines) > 0 {
			if text == "nothing to do" {
				text = ""
			} else {
				text += "\n"
			}
			text += strings.Join(lines, "\n")
		}
		return Result{Data: d, Text: text}, nil
	})
}

// runAutopilotTick runs one tick for the lease holder. The lease is checked on
// every tick: wind-down or a takeover ends the autopilot cleanly.
func runAutopilotTick(c *Ctx, root string, set roundcfg.Settings, baseOverride string, pid int, observe bool) (roundtick.Result, error) {
	c.deps().freshReads()
	ctx := c.Context()
	c.holderPID = pid
	renv := accountedRoundEnv(c, ctx, root)
	lease, cd, held, err := renv.HeldLeaseDir(ctx, root)
	if err != nil {
		return roundtick.Result{}, err
	}
	if !held {
		return roundtick.Result{}, roundtick.ErrNoLease
	}
	policy, err := mergePolicy(c)
	if err != nil {
		return roundtick.Result{}, err
	}
	renv = withBoard(c, root, renv)
	be, err := openBacklog(c, root, false, "")
	if err != nil {
		_, ferr := backlogFail(err)
		return roundtick.Result{}, ferr
	}
	board, ok := round.BoardOf(be)
	if !ok {
		return roundtick.Result{}, &Error{Exit: ExitInternal, Message: "the backlog backend has no workflow"}
	}
	return roundtick.RunPass(ctx, roundtick.Pass{
		CommonDir: cd, Round: lease.Round, Observe: observe,
		Config: roundtick.Config{Settings: set, MergeApproval: policy.Mode, Base: renv.Base, BaseOverride: baseOverride},
		World: func(st roundtick.State, observe bool) roundtick.World {
			return &tickWorld{c: c, root: root, set: set, renv: renv.Inspecting(observe), be: be, board: board, state: st, commonDir: cd, round: lease.Round, observe: observe}
		},
	})
}

// quotaCapped is the tick's cap hook: why no slot may fill, "" when one may.
func quotaCapped(renv round.Env, root string, set roundcfg.Settings) func(context.Context) string {
	return func(ctx context.Context) string { return renv.QuotaCap(ctx, root, set).Reason }
}

func tickData(r roundtick.Result) *jsonx.Object {
	d := jsonx.NewObject()
	if r.Stopped != nil {
		d.Set("stopped", true)
		d.Set("stoppedReason", r.Stopped.Error())
	}
	if len(r.Holding) > 0 {
		d.Set("holding", jsonx.Strings(r.Holding))
	}
	if len(r.FillStatus) > 0 {
		d.Set("fillStatus", jsonx.Strings(r.FillStatus))
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
	if r.Observed {
		d.Set("observed", true)
		d.Set("notice", observationNotice)
		proposed := make([]any, 0, len(r.Proposed))
		for _, p := range r.Proposed {
			o := jsonx.NewObject()
			o.Set("action", p.Action.Action)
			o.Set("target", p.Target)
			o.Set("detail", p.Detail)
			o.Set("conditional", p.Conditional)
			o.Set("unchecked", jsonx.Strings(p.Unchecked))
			proposed = append(proposed, o)
		}
		d.Set("proposed", proposed)
	}
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
	if r.Observed {
		d.Set("unknown", items(r.Unknown))
	}
	if len(r.Holds) > 0 {
		d.Set("holds", items(r.Holds))
	}
	d.Set("needsYou", items(r.NeedsYou))
	d.Set("new", items(r.New))
	return d
}

func tickLines(r roundtick.Result) string {
	var lines []string
	if r.Stopped != nil {
		lines = append(lines, "stopped mid-tick: "+r.Stopped.Error())
	}
	if r.Observed {
		lines = append(lines, observationNotice)
		for _, p := range r.Proposed {
			kind := "proposed"
			if p.Conditional {
				kind = "proposed (conditional)"
			}
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s\tunchecked: %s", kind, p.Action.Action, p.Target, p.Detail, strings.Join(p.Unchecked, "; ")))
		}
		for _, u := range r.Unknown {
			lines = append(lines, fmt.Sprintf("unknown\t%s\t%s\t%s", u.Kind, u.Target, u.Why))
		}
	}
	lines = append(lines, r.Holding...)
	for _, a := range r.Did {
		lines = append(lines, fmt.Sprintf("did\t%s\t%s\t%s", a.Action, a.Target, dash(a.Detail)))
	}
	for _, it := range r.Holds {
		lines = append(lines, fmt.Sprintf("hold\t%s\t%s", it.Target, it.Why))
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

// reviewSources wires the forge, registry and config into the
// roundtick.ReviewSources of one slot's review loop. The caller decides
// whether the pass observes.
func reviewSources(c *Ctx, root string, set roundcfg.Settings, slot string) roundtick.ReviewSources {
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
		return src
	}
	src.Poll = func(ctx context.Context) (string, error) {
		return roundtick.PendingSummary(ctx, o.Forge, s, o.Verdict, c.deps().Now())
	}
	src.Relay = func(ctx context.Context) (roundtick.RelayResult, error) {
		res, err := reviewEnv(c, ctx, root, o).ReviewRelay(ctx, root, o)
		return roundtick.RelayResultOf(res.Nothing, res.Items, res.Bounces, err), err
	}
	return src
}

// reviewStep runs one review-loop pass over a done slot (the watch's use).
func reviewStep(c *Ctx, root string, set roundcfg.Settings, slot string) roundtick.ReviewOutcome {
	return roundtick.ReviewStep(c.Context(), reviewSources(c, root, set, slot))
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
	return r.renv.MintReview(ctx, r.root, r.board, a, r.renv.CurrentRound(ctx, r.root))
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
	// commonDir and round locate the stored stop value Stop re-reads.
	commonDir string
	round     int
	// observe selects the no-write variants of the reads that otherwise
	// cache or repair state; roundtick.NewEnv refuses every write.
	observe bool
}

func (w *tickWorld) Observing() bool { return w.observe }

func (w *tickWorld) TolerantRegistry() worker.Registry  { return worker.LoadRegistryTolerant(w.root) }
func (w *tickWorld) Registry() (worker.Registry, error) { return worker.LoadRegistry(w.root) }
func (w *tickWorld) Forge(ctx context.Context) (roundtick.PRViewer, error) {
	return w.c.deps().forge(ctx, w.c.deps().Config(w.root), "", w.root)
}

// FreshForge skips the read cache: the tick reads a queued PR's head after the
// gate, and a cached head would hide a push made while the gate ran.
func (w *tickWorld) FreshForge(ctx context.Context) (roundtick.PRViewer, error) {
	d := w.c.deps()
	return tracker.NewForgeFromConfig(ctx, d.Config(w.root), "", w.root, d.trackerOptions(uncachedReads("a queued PR's head is re-read after a gate that ran for minutes; a cached head hides a push made meanwhile"))...)
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
	if w.observe {
		return w.renv.InspectReconcile(ctx, w.root)
	}
	return w.renv.Reconcile(ctx, w.root, true)
}

func (w *tickWorld) Root() string { return w.root }

// Stop re-reads the stored stop value: state is the snapshot taken at tick
// entry, and a wind-down can set the flag after it.
func (w *tickWorld) Stop() (bool, string) {
	return w.renv.Stop.Load(w.commonDir, w.round)
}

func (w *tickWorld) Landing(ctx context.Context, req postgate.Request) (postgate.Env, postgate.Request, error) {
	if req.Gate != nil {
		o := req.Gate
		return prepareGate(w.c, ctx, w.root, gateRunOpts{Slot: o.Slot, Base: o.Base, NoWait: o.NoWait})
	}
	o := req.Train
	return prepareTrain(w.c, ctx, w.root, trainRunOpts{Targets: o.Targets, Base: o.Base, NoWait: o.NoWait})
}

// ReviewReadiness says why a done slot still waits for required reviews, and
// whether its identity/state verdict must go straight to the real gate; an
// incomplete probe or a read error cannot confirm readiness. An observing
// world asks the gate for its no-write variant (no fetch, no verdict-report
// recovery).
func (w *tickWorld) ReviewReadiness(slot, base string) (string, bool) {
	observe := w.observe
	ctx, stop := w.c.workerContext()
	defer stop()
	r, err := workerEnvCtx(w.c, ctx).Gate(ctx, w.root, worker.GateOpts{Slot: slot, Base: base, ReviewsOnly: true, Observe: observe, VerdictRepo: repos.VerdictRepo(w.root, w.root, w.c.Repo), ItemBody: lazyItemBody(w.c, w.root)})
	return roundtick.ReviewReadinessOf(r, err, slot)
}

func (w *tickWorld) ReviewLoop(slot string) roundtick.ReviewOutcome {
	src := reviewSources(w.c, w.root, w.set, slot)
	src.Observe = w.observe
	return roundtick.ReviewStep(w.c.Context(), src)
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
	return w.assign(ctx, round.AssignOpts{ID: id})
}

// AssignBundle is Assign for a lead and its members: one slot, one branch.
func (w *tickWorld) AssignBundle(ctx context.Context, lead string, members []string) (round.Assigned, error) {
	return w.assign(ctx, round.AssignOpts{ID: lead, Bundle: members})
}

// AssignTo is Assign onto a named slot (`round assign --agent`): the
// autopilot's assign-ahead of a done slot, whose PR it queues (#4247).
func (w *tickWorld) AssignTo(ctx context.Context, slot, lead string, members []string) (round.Assigned, error) {
	return w.assign(ctx, round.AssignOpts{ID: lead, Bundle: members, Agent: slot})
}

func (w *tickWorld) assign(ctx context.Context, o round.AssignOpts) (round.Assigned, error) {
	return w.renv.Assign(ctx, w.root, w.board, o)
}

func (w *tickWorld) Review() roundtick.Reviewer {
	r := reviewAdapter{c: w.c, root: w.root, set: w.set, renv: w.renv, board: w.board}
	if w.observe {
		return inspectingReviewer{r}
	}
	return r
}

// TestAudit reads the recorded test-audit thresholds, read-only: no listing
// commands, no checkpoint write.
func (w *tickWorld) TestAudit(context.Context) ([]string, error) {
	t, err := readTestAuditTelemetry(w.c, w.root, w.c.deps().Config(w.root))
	if err != nil || !t.SuggestMaintenance {
		return nil, err
	}
	return t.Reasons, nil
}

// FollowUp wires the follow-up step (#2580) to the forge and the transfer
// verb; roundtick owns what it does with them.
func (w *tickWorld) FollowUp() roundtick.FollowUpSources {
	return roundtick.NewFollowUpSources(roundtick.FollowUpPorts{
		Settings: w.set,
		Registry: w.TolerantRegistry,
		Forge: func(ctx context.Context) (round.ReviewForge, error) {
			fg, err := w.c.deps().forge(ctx, w.c.deps().Config(w.root), "", w.root)
			if err != nil {
				return nil, trackerErr(err)
			}
			return fg, nil
		},
		List: func(ctx context.Context, fg round.ReviewForge) ([]round.FollowUp, []string, error) {
			return w.renv.FollowUps(ctx, w.root, fg)
		},
		Start: func(ctx context.Context, f round.FollowUp, slot string, reserve bool) ([]string, error) {
			res, err := w.renv.Continue(ctx, w.root, w.board, round.ContinueOpts{FollowUp: f, To: slot, Reserve: reserve, MaxBounces: w.set.MaxBounces})
			return res.Warnings, err
		},
		StartFailed: func(ctx context.Context, pr, why, slot string, reserve bool) ([]string, error) {
			res, err := w.renv.ContinueFailed(ctx, w.root, w.board, round.ContinueFailedOpts{PR: pr, Why: why, To: slot, Reserve: reserve})
			return res.Warnings, err
		},
		Warn: func(m string) { w.c.Warn("%s", m) },
	})
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
