package roundtick

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// PRViewer is the slice of the forge the tick reads: one PR's state, base and head.
type PRViewer interface {
	PRView(ctx context.Context, n int) (tracker.PRInfo, error)
}

// ReadyPR is a queued PR as the train-fill rule sees it: reviewed at its
// current head, since when, and whether it must skip the fill wait.
type ReadyPR struct {
	PR         string
	Reviewed   bool
	ReadySince time.Time
	Immediate  bool
}

// World is every outside seam a tick reads or acts through. The CLI
// implements its transport adapters; tests fake external boundaries.
type World interface {
	// TolerantRegistry never fails: a tick callback has no error return, and
	// the tick reconciles strictly. Registry is the strict read.
	TolerantRegistry() worker.Registry
	Registry() (worker.Registry, error)
	Forge(ctx context.Context) (PRViewer, error)
	// Waiting lists the queued PRs with their readiness. The tick reads it at
	// most once, after reconciliation.
	Waiting() []ReadyPR
	Reconcile(ctx context.Context) (round.Outcome, error)
	// Landing prepares dependencies and options only. NewEnv owns invoking
	// the domain lifecycle, so adapters cannot replace its ordering.
	Landing(context.Context, postgate.Request) (postgate.Env, postgate.Request, error)
	Root() string
	// ReviewReadiness says why a done slot still waits for required reviews,
	// and whether its state must go straight to the real gate.
	ReviewReadiness(slot, base string) (why string, gateNow bool)
	ReviewLoop(slot string) ReviewOutcome
	Capped(ctx context.Context) string
	OpenItems() int
	Candidates(ctx context.Context) ([]round.Candidate, error)
	Assign(ctx context.Context, id string) (round.Assigned, error)
	Review() Reviewer
	Audit(Action)
	Say(string)
	Warn(string)
	Now() time.Time
}

// Config is the tick's settings: what the round is set to, not what it reads.
type Config struct {
	Settings      roundcfg.Settings
	MergeApproval string
	BaseOverride  string
	Base          string
}

// wiring owns the state the closures of one tick share: which targets must
// skip the fill wait, and the memoized readiness read.
type wiring struct {
	w       World
	e       *Env
	bypass  map[string]bool
	ready   map[int]ReadyPR
	readied bool
}

// NewEnv builds the Env of one tick from the seams. It owns the rule that a
// closed PR or one whose base moved goes straight to an individual gate
// instead of waiting for the train to fill.
func NewEnv(w World, cfg Config) Env {
	set := cfg.Settings
	e := Env{
		Cap: set.AutopilotCap, MergeApproval: cfg.MergeApproval, Batch: set.Batch, TrainFill: set.TrainFill,
		Now: w.Now, Role: set.Role, Base: cfg.Base, BaseOverride: cfg.BaseOverride,
	}
	k := &wiring{w: w, e: &e, bypass: map[string]bool{}}
	e.Capped = w.Capped
	e.Slots = func() []Slot { return SlotsOf(w.TolerantRegistry().Slots()) }
	e.Active = func() []string {
		active, _ := round.Members(w.TolerantRegistry(), set)
		return active
	}
	e.Queued = func() []string { return QueuedRefs(w.TolerantRegistry().PRs()) }
	e.Reconcile = func(ctx context.Context) ([]string, []string, error) {
		out, err := w.Reconcile(ctx)
		if err != nil {
			return nil, nil, err
		}
		repaired, drift := ReconcileLines(out)
		return repaired, drift, nil
	}
	e.Target = func(target string) (worker.GateTarget, error) {
		reg, err := w.Registry()
		if err != nil {
			return worker.GateTarget{}, err
		}
		return reg.GateTarget(target)
	}
	e.TrainReady = func(target string) bool {
		_, ok := k.lookup(target)
		return ok
	}
	e.ReadySince = func(target string) time.Time {
		p, _ := k.lookup(target)
		return p.ReadySince
	}
	e.FillBypass = func(target string) bool {
		if k.bypass[target] {
			return true
		}
		p, ok := k.lookup(target)
		return ok && p.Immediate
	}
	e.Head = k.head
	e.RunGate = func(ctx context.Context, target, base string) GateRun {
		env, req, err := w.Landing(ctx, postgate.Request{Gate: &worker.GateOpts{Slot: target, Base: base, NoWait: true}})
		if err != nil {
			return GateRun{Err: err}
		}
		run := env.Run(ctx, w.Root(), req).Gate
		for _, warning := range run.Warnings {
			w.Warn(warning)
		}
		return run
	}
	e.AwaitingReview = func(_ context.Context, sl Slot, base string) string {
		why, now := w.ReviewReadiness(sl.Name, base)
		k.bypass[sl.Name] = now
		return why
	}
	e.RunTrain = func(ctx context.Context, targets []string, base string) TrainRun {
		env, req, err := w.Landing(ctx, postgate.Request{Train: &worker.TrainOpts{Targets: targets, Base: base, NoWait: true}})
		if err != nil {
			return TrainRun{Err: err}
		}
		run := env.Run(ctx, w.Root(), req).Train
		for _, warning := range run.Warnings {
			w.Warn(warning)
		}
		return run
	}
	e.Say = w.Say
	e.OpenItems = func(context.Context) int { return w.OpenItems() }
	e.Candidates = w.Candidates
	e.Review = w.Review()
	e.Assign = func(ctx context.Context, id string) ([]string, error) {
		res, err := w.Assign(ctx, id)
		if err != nil {
			return nil, err
		}
		return AssignedAgents(res), nil
	}
	e.ReviewLoop = func(_ context.Context, sl Slot) ReviewOutcome { return w.ReviewLoop(sl.Name) }
	e.Audit = w.Audit
	return e
}

// lookup finds a target's reviewed PR. The waiting list is read once.
func (k *wiring) lookup(target string) (ReadyPR, bool) {
	if !k.readied {
		k.readied = true
		k.ready = map[int]ReadyPR{}
		for _, p := range k.w.Waiting() {
			if n, ok := worker.PRRefNumber(p.PR); ok && p.Reviewed {
				k.ready[n] = p
			}
		}
	}
	t, err := k.e.Target(target)
	if err != nil {
		return ReadyPR{}, false
	}
	n, _ := worker.PRRefNumber(t.PR)
	p, ok := k.ready[n]
	return p, ok
}

// head reads a target's PR identity and head SHA. A PR that is no longer open,
// or whose base is not the one the tick would merge into, is marked to go
// straight to the gate, which owns what to do about it.
func (k *wiring) head(ctx context.Context, target string) (string, error) {
	t, err := k.e.Target(target)
	if err != nil {
		return "", err
	}
	n, ok := worker.PRRefNumber(t.PR)
	if !ok {
		return "", fmt.Errorf("cannot read PR number from %q", t.PR)
	}
	forge, err := k.w.Forge(ctx)
	if err != nil {
		return "", err
	}
	info, err := forge.PRView(ctx, n)
	if err != nil {
		return "", err
	}
	if prState := strings.ToLower(info.State); prState != "" && prState != "open" {
		k.bypass[target] = true
	}
	if info.Base != "" && info.Base != strutil.FirstNonEmpty(k.e.BaseOverride, t.Base, k.e.Base) {
		k.bypass[target] = true
	}
	if info.HeadSHA == "" {
		if k.bypass[target] {
			return fmt.Sprintf("%d:state", n), nil
		}
		return "", fmt.Errorf("PR %s has no head SHA", t.PR)
	}
	return fmt.Sprintf("%d:%s", n, info.HeadSHA), nil
}
