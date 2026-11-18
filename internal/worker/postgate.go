package worker

import (
	"context"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/postgate"
)

// Landing adapts round execution and state to the postgate lifecycle ports.
func (e Env) Landing(release func(root, issue, slot string) error) postgate.Env {
	return postgate.Env{Runner: landingRunner{e}, Git: e.Git, Book: landingBook{e, release}}
}

type landingRunner struct{ env Env }

func (a landingRunner) Gate(ctx context.Context, root string, o postgate.GateOpts) (GateResult, error) {
	return a.env.Gate(ctx, root, GateOpts{NoWait: o.NoWait, Slot: o.Slot, Base: o.Base, CheckOnly: o.CheckOnly, NoVerify: o.NoVerify, FinalSlice: o.FinalSlice, Prune: o.Prune, Round: o.Round, Approval: o.Approval, VerdictRepo: o.VerdictRepo, CheckReviews: o.CheckReviews, ItemBody: o.ItemBody})
}

func (a landingRunner) Train(ctx context.Context, root string, o postgate.TrainOpts) (TrainResult, error) {
	return a.env.Train(ctx, root, TrainOpts{NoWait: o.NoWait, Targets: o.Targets, Round: o.Round, Base: o.Base, SyncBase: o.SyncBase, LandGreen: o.LandGreen, OnFail: o.OnFail, NoVerify: o.NoVerify, FinalSlice: o.FinalSlice, Approve: o.Approve, VerdictRepo: o.VerdictRepo, CheckReviews: o.CheckReviews, Order: o.Order, Say: o.Say, Plan: o.Plan, ItemBody: o.ItemBody, Integration: o.Integration, Onto: o.Onto, RequestID: o.RequestID})
}

type landingBook struct {
	env     Env
	release func(root, issue, slot string) error
}

func (b landingBook) ReleaseSlice(root, issue, slot string) error {
	if b.release == nil || slot == "" {
		return nil
	}
	return b.release(root, issue, slot)
}
func (b landingBook) ClearItemStart(root, issue string) error { return ClearItemStart(root, issue) }
func (b landingBook) ClearBestOf(root, issue string) error    { return ClearBestOf(root, issue) }
func (b landingBook) ClearBounces(root, issue string) error {
	store, err := b.env.BounceStore(root)
	if err != nil {
		return err
	}
	return store.Clear(issue)
}
func (b landingBook) PostReviewBounce(root, branch, pr, head, why string) error {
	_, err := b.env.PostReviewBounce(root, branch, pr, head, why)
	return err
}
func (b landingBook) Targets(root string, args []string) (map[string]postgate.GateTarget, error) {
	targets, err := ResolveGateTargets(root, args)
	if err != nil {
		return nil, err
	}
	out := make(map[string]postgate.GateTarget, len(targets))
	for name, t := range targets {
		out[name] = postgate.GateTarget{Name: t.Name, Branch: t.Branch, PR: t.PR, Issue: t.Issue}
	}
	return out, nil
}
func (b landingBook) HasOrigin(ctx context.Context, root string) bool {
	res, err := (git.Repo{Dir: root, Runner: b.env.Git}).Run(ctx, "remote", "get-url", "origin")
	return err == nil && res.ExitCode == 0
}
