package roundtick

import (
	"context"
	"errors"
	"time"

	"github.com/l4ci/rota/internal/postgate"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

var errReadOnlyWorld = errors.New("read-only world: observation never changes state")

// readOnlyWorld is the only write-refusal seam. The underlying observing World
// supplies no-write variants of reads that ordinarily cache or repair state.
// No embedding: each new World method needs an explicit read/write decision.
type readOnlyWorld struct{ w World }

var _ World = readOnlyWorld{}

func (o readOnlyWorld) Observing() bool                             { return true }
func (o readOnlyWorld) TolerantRegistry() worker.Registry           { return o.w.TolerantRegistry() }
func (o readOnlyWorld) Registry() (worker.Registry, error)          { return o.w.Registry() }
func (o readOnlyWorld) Forge(ctx context.Context) (PRViewer, error) { return o.w.Forge(ctx) }
func (o readOnlyWorld) FreshForge(ctx context.Context) (PRViewer, error) {
	return o.w.FreshForge(ctx)
}
func (o readOnlyWorld) Waiting() []ReadyPR { return o.w.Waiting() }
func (o readOnlyWorld) Reconcile(ctx context.Context) (round.Outcome, error) {
	return o.w.Reconcile(ctx)
}
func (o readOnlyWorld) Landing(context.Context, postgate.Request) (postgate.Env, postgate.Request, error) {
	return postgate.Env{}, postgate.Request{}, errReadOnlyWorld
}
func (o readOnlyWorld) Root() string { return o.w.Root() }
func (o readOnlyWorld) ReviewReadiness(slot, base string) (string, bool) {
	return o.w.ReviewReadiness(slot, base)
}
func (o readOnlyWorld) ReviewLoop(slot string) ReviewOutcome { return o.w.ReviewLoop(slot) }
func (o readOnlyWorld) Capped(ctx context.Context) string    { return o.w.Capped(ctx) }
func (o readOnlyWorld) OpenItems() int                       { return o.w.OpenItems() }
func (o readOnlyWorld) Candidates(ctx context.Context) ([]round.Candidate, error) {
	return o.w.Candidates(ctx)
}
func (o readOnlyWorld) Assign(context.Context, string) (round.Assigned, error) {
	return round.Assigned{}, errReadOnlyWorld
}
func (o readOnlyWorld) AssignBundle(context.Context, string, []string) (round.Assigned, error) {
	return round.Assigned{}, errReadOnlyWorld
}
func (o readOnlyWorld) AssignTo(context.Context, string, string, []string) (round.Assigned, error) {
	return round.Assigned{}, errReadOnlyWorld
}
func (o readOnlyWorld) Review() Reviewer {
	r := o.w.Review()
	if r == nil {
		return nil
	}
	return readOnlyReviewer{r}
}
func (o readOnlyWorld) TestAudit(ctx context.Context) ([]string, error) { return o.w.TestAudit(ctx) }
func (o readOnlyWorld) Stop() (bool, string)                            { return o.w.Stop() }
func (o readOnlyWorld) FollowUp() FollowUpSources {
	f := o.w.FollowUp()
	if f.Start != nil {
		f.Start = func(context.Context, FollowUp, string, bool) error { return errReadOnlyWorld }
	}
	if f.StartFailed != nil {
		f.StartFailed = func(context.Context, string, string, string, bool) error { return errReadOnlyWorld }
	}
	return f
}
func (o readOnlyWorld) Audit(Action)        {}
func (o readOnlyWorld) Say(line string)     { o.w.Say(line) }
func (o readOnlyWorld) Warn(message string) { o.w.Warn(message) }
func (o readOnlyWorld) Now() time.Time      { return o.w.Now() }

// Facts is a read supplied by the observing adapter; minting is always refused.
type readOnlyReviewer struct{ r Reviewer }

var _ Reviewer = readOnlyReviewer{}

func (r readOnlyReviewer) Facts(ctx context.Context) (round.Architecture, bool, error) {
	return r.r.Facts(ctx)
}
func (r readOnlyReviewer) Mint(context.Context, round.Architecture) ([]string, error) {
	return nil, errReadOnlyWorld
}
