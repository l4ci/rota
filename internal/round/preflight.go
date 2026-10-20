package round

import (
	"context"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// preflightIn is what Assign and Transfer hand the shared preflight: the item,
// the settings, the tier the worker starts on and what each verb knows about
// the worker's kind and model. Transfer sets no flags: it has no --kind or
// --model.
type preflightIn struct {
	Item     *backlog.Item
	Settings roundcfg.Settings
	Tier     string
	// FlagKind and FlagModel are --kind and --model; FlagModel beats the
	// item's model: label and the tier map.
	FlagKind, FlagModel string
	// SlotKind is the receiving slot's recorded harness kind.
	SlotKind string
	// Fixed pins the kind and its source (a best-of:2 attempt whose pair
	// settled its kinds together); it replaces the resolved kind.
	Fixed *bestOfRun
	// Resuming is a slot that already holds the item: it has its account and
	// its place in the scope, so neither is checked again.
	Resuming bool
}

// preflightOut is the resolved launch of one worker. Fields fill in order, so
// a refusal leaves the earlier ones set for the caller's result.
type preflightOut struct {
	Pick             Pick
	Decision         string // the item's decision type, "" for an ordinary item
	Kind, KindSource string
	Model            string // resolved: what the delivery refusals check
	AppliedModel     string // Model, or "" when the launch command has no {model}
	Warnings         []string
}

// preflight answers "can this item go to this slot" for assign and transfer:
// the item's pick, the worker kind and model, the account headroom, whether
// the model reaches the launch line and whether the scope allows the item.
// It refuses with a typed BlockedError before anything moves.
func (e Env) preflight(ctx context.Context, root string, be backlog.Backend, in preflightIn) (out preflightOut, err error) {
	set, id := in.Settings, in.Item.ID
	out.Pick, err = PickOf(be.Capabilities(), *in.Item)
	if err != nil {
		return out, err
	}
	if out.Decision, err = DecisionOf(be.Capabilities(), *in.Item); err != nil {
		return out, err
	}
	if out.Decision == DecisionGrilling {
		return out, blocked(BlockHumanDecision, "%s is a grilling decision: the human resolves it, one per session; run /rota-brainstorm %s", id, id)
	}
	kind, kindSource := resolveKind(in.FlagKind, out.Pick.Harness, set.WorkerKind, in.SlotKind)
	if in.Fixed != nil {
		kind, kindSource = in.Fixed.kind, in.Fixed.kindSource
	}
	hz, err := worker.Harness(kind)
	if err != nil {
		return out, err
	}
	kind = hz.Kind()
	out.Kind, out.KindSource = kind, kindSource
	// An unset codex tier map is allowed: the default codex command drops
	// --model and Codex picks its own. A custom round.codexCommand holding
	// {model} would fail at dispatch, after the claim, so it is refused here.
	model := resolveModel(in.FlagModel, out.Pick.Model, set.Model(kind, in.Tier))
	if model == "" && e.workerEnv().NeedsModel(root, kind) {
		return out, blocked(BlockNoTierMap, "round.tiers.%s has no model for the %s tier: set round.tiers.%s.*", kind, in.Tier, kind)
	}
	out.Model, out.AppliedModel = model, model
	if !in.Resuming {
		if why := e.Headroom(ctx, root, kind); why != "" {
			return out, blocked(BlockQuota, "%s", why)
		}
	}
	if !e.workerEnv().ModelAppliesTo(root, kind) {
		// A model asked for by name must reach the launch line; the tier map's
		// is a default and only warns.
		if in.FlagModel != "" || out.Pick.Model != "" {
			src := "model:" + out.Pick.Model + " label"
			if in.FlagModel != "" {
				src = "--model " + in.FlagModel
			}
			return out, blocked(BlockModelLabel, "%s cannot apply: %s has no {model} placeholder", src, hz.CommandKey())
		}
		out.AppliedModel = ""
		out.Warnings = append(out.Warnings, "tier model not applied: "+hz.CommandKey()+" has no {model} placeholder")
	}

	scope, slate := ScopeOf(root, set, "")
	if !in.Resuming {
		ok, err := InScope(root, be, scope, slate, id)
		if err != nil {
			return out, err
		}
		if !ok {
			return out, blocked(BlockOutOfScope, "%s is outside the round's scope (%s)", id, scope)
		}
	}
	return out, nil
}
