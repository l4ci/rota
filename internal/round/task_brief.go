package round

import (
	"context"
	"fmt"

	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/skills"
	"github.com/l4ci/rota/internal/worker"
)

// taskBriefSpec is the context shared by assignment and operator recovery.
// Issue sections are always read from the backlog; the binding keeps the
// assignment's settled answers and siblings, which cannot be recovered from labels.
type taskBriefSpec struct {
	agent, id, branch, contract, decisions string
	decisionContext                        string
	siblings, bundle                       []string
	tier                                   tierBrief
}

func (e Env) taskBrief(ctx context.Context, root string, be Board, in taskBriefSpec) string {
	steps, withheld := e.tryItBrief(ctx, root, be, in.id)
	var bundle []bundleIssue
	if len(in.bundle) > 0 {
		bundle = bundleBrief(be, append([]string{in.id}, in.bundle...))
	}
	remote, _ := e.remote(root) // Assign and Transfer refuse an invalid git.remote before a brief is built
	in.tier.Remote = remote
	text := pointerBrief(in.agent, in.id, in.branch, in.contract, in.siblings, in.decisions, in.decisionContext,
		outOfScope(be, in.id), touches(be, in.id), steps, withheld, bundle, in.tier)
	if hb := latestHandoffBranch(be, in.id); hb != "" {
		if remote == "" {
			remote = "origin"
		}
		text += fmt.Sprintf("\nAn earlier worker handed this issue back: read the latest `rota:handoff` comment on it. Its work is pushed on branch %s (%s/%s); fetch it before you start over.\n", hb, remote, hb)
	}
	return text
}

// RebuildTaskBrief reconstructs an assigned task without taking another claim,
// moving its branch or minting another attempt. Dispatch still owns preflight,
// quota and the reset guard, all before the old session is killed.
func (e Env) RebuildTaskBrief(ctx context.Context, root string, be Board, o worker.DispatchOpts, set roundcfg.Settings) (string, worker.DispatchOpts, error) {
	reg, err := worker.LoadRegistry(root)
	if err != nil {
		return "", o, err
	}
	s := reg.Slot(o.Slot)
	if s == nil || s.Task() != worker.NormID(o.Task) || s.ClaimID() == "" {
		return "", o, blocked(BlockSlotBusy, "slot %s is not assigned %s", o.Slot, o.Task)
	}
	it, err := be.Get(s.Task())
	if err != nil {
		return "", o, err
	}
	pick, err := PickOf(be.Capabilities(), *it)
	if err != nil {
		return "", o, err
	}
	decision, err := DecisionOf(be.Capabilities(), *it)
	if err != nil {
		return "", o, err
	}
	cacheDir := skills.CacheDir(e.getenv())
	contract, _, ok := resolveBrief(root, set, e.getenv(), cacheDir)
	if !ok {
		return "", o, blocked(BlockBriefMissing, "%s", briefMissingMsg(set, cacheDir))
	}
	if o.Kind == "" {
		o.Kind = s.HarnessKind()
	}
	if o.Model == "" {
		o.Model = resolveModel(s.Model(), pick.Model, set.Model(o.Kind, s.Tier()))
	}
	if pick.Model != "" && !e.Worker.ModelAppliesTo(root, o.Kind) {
		return "", o, blocked(BlockModelLabel, "model:%s cannot apply: the launch command has no {model} placeholder", pick.Model)
	}
	o.Task, o.Branch = s.Task(), s.Branch()
	t := tierBrief{Kind: o.Kind, Tier: s.Tier(), Model: o.Model, Default: set.Tier, Reason: s.TierReason(),
		Table: set.Models[o.Kind], Pick: pick.String(), Decision: decision, Smoke: s.SmokeSection(), Attempt: s.Attempt(), FullOnly: set.FullOnly, TDD: set.TDD}
	if bo := reg.BestOf(s.Task()); bo != nil {
		for _, other := range bo.Attempts {
			if other.Slot != o.Slot {
				t.Sibling, t.SiblingBranch = other.Slot, other.Branch
			}
		}
	}
	return e.taskBrief(ctx, root, be, taskBriefSpec{agent: o.Slot, id: s.Task(), branch: o.Branch, contract: contract,
		decisions: s.BriefSettledAnswers(), decisionContext: s.BriefDecisionContext(), siblings: s.BriefSiblings(), bundle: s.Bundle(), tier: t}), o, nil
}
