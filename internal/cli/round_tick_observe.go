package cli

import (
	"context"
	"errors"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/worker"
)

const observationNotice = "Observation only: not approval or landing proof; execution re-reads state and checks every gate."

func tickNudge(c *Ctx, root string, d *jsonx.Object, observed bool) []string {
	if observed {
		return nil
	}
	return addTrainNudge(c, root, d, nil)
}

// errReadOnlyWorld is what an observing tickWorld answers to anything that
// would change the world; roundtick.Run proposes those steps instead of
// calling them, so reaching one is a bug the refusal makes visible.
var errReadOnlyWorld = errors.New("read-only world: observation never changes state")

// refuse is the only place errReadOnlyWorld is returned.
func refuse() error { return errReadOnlyWorld }

// observingWorld is the read-only seam of the tick: the one place observation
// mode is enforced. It wraps a tickWorld and implements roundtick.World
// explicitly (no embedding), so a method added to the interface fails to
// compile here until someone decides whether observation may reach it. Reads
// delegate or take their no-write variant; every call that would change state
// is refused or, for the audit line, dropped.
type observingWorld struct{ w *tickWorld }

var _ roundtick.World = observingWorld{}

func (o observingWorld) TolerantRegistry() worker.Registry  { return o.w.TolerantRegistry() }
func (o observingWorld) Registry() (worker.Registry, error) { return o.w.Registry() }
func (o observingWorld) Forge(ctx context.Context) (roundtick.PRViewer, error) {
	return o.w.Forge(ctx)
}
func (o observingWorld) Waiting() []roundtick.ReadyPR { return o.w.Waiting() }
func (o observingWorld) Reconcile(ctx context.Context) (round.Outcome, error) {
	return o.w.renv.InspectReconcile(ctx, o.w.root)
}
func (o observingWorld) Landing(context.Context, postgate.Request) (postgate.Env, postgate.Request, error) {
	return postgate.Env{}, postgate.Request{}, refuse()
}
func (o observingWorld) Root() string { return o.w.Root() }
func (o observingWorld) ReviewReadiness(slot, base string) (string, bool) {
	return o.w.reviewReadiness(slot, base, true)
}

// ReviewLoop reads pending review input without invoking the relay, and its
// relay refuses anyway, so the guarantee holds even if a pass ignores Observe.
func (o observingWorld) ReviewLoop(slot string) roundtick.ReviewOutcome {
	src := reviewSources(o.w.c, o.w.root, o.w.set, slot)
	src.Observe = true
	if src.Relay != nil {
		src.Relay = func(context.Context) (roundtick.RelayResult, error) {
			return roundtick.RelayResult{}, refuse()
		}
	}
	return roundtick.ReviewStep(o.w.c.Context(), src)
}
func (o observingWorld) Capped(ctx context.Context) string { return o.w.Capped(ctx) }
func (o observingWorld) OpenItems() int                    { return o.w.OpenItems() }
func (o observingWorld) Candidates(ctx context.Context) ([]round.Candidate, error) {
	return o.w.Candidates(ctx)
}
func (o observingWorld) AssignBundle(context.Context, string, []string) (round.Assigned, error) {
	return round.Assigned{}, refuse()
}

func (o observingWorld) Assign(context.Context, string) (round.Assigned, error) {
	return round.Assigned{}, refuse()
}
func (o observingWorld) Review() roundtick.Reviewer {
	return observingReviewer{o.w.Review().(reviewAdapter)}
}
func (o observingWorld) FollowUp() roundtick.FollowUpSources {
	f := o.w.FollowUp()
	f.Start = func(context.Context, roundtick.FollowUp, string, bool) error { return refuse() }
	return f
}
func (o observingWorld) Audit(roundtick.Action) {}
func (o observingWorld) Say(line string)        { o.w.Say(line) }
func (o observingWorld) Warn(message string)    { o.w.Warn(message) }
func (o observingWorld) Now() time.Time         { return o.w.Now() }

// observingReviewer reads the architecture facts without the write the real
// read does, and refuses to mint.
type observingReviewer struct{ reviewAdapter }

func (r observingReviewer) Facts(ctx context.Context) (round.Architecture, bool, error) {
	cands, err := r.renv.RoundCandidates(ctx, r.root, r.board, r.set, "")
	if err != nil {
		return round.Architecture{}, false, err
	}
	return r.renv.InspectArchitectureFacts(ctx, r.root, r.board, r.set, cands)
}
func (r observingReviewer) Mint(context.Context, round.Architecture) ([]string, error) {
	return nil, refuse()
}
