package round

import (
	"context"
	"fmt"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/worker"
)

// preflightIn is what Assign and Transfer hand the shared preflight: the item,
// the settings, the tier the worker starts on and what each verb knows about
// the worker's kind and model.
type preflightIn struct {
	Item     *backlog.Item
	Settings roundcfg.Settings
	Tier     string
	// FlagKind and FlagModel are --kind and --model; FlagModel beats the
	// item's model: label and the tier map.
	FlagKind, FlagModel string
	// Slot, SlotKind and SlotAccount are the receiving slot, its recorded
	// harness kind and the account it ran under last.
	Slot, SlotKind, SlotAccount string
	// Fixed pins the kind and its source (a best-of:2 attempt whose pair
	// settled its kinds together); it replaces the resolved kind.
	Fixed *bestOfRun
	// NoFallback keeps the receiver's kind and account unless its account is
	// cooling (transfer).
	NoFallback bool
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
	// Placement is where the worker goes: nil when the project has no pool to
	// balance (no round.harnesses and no round.accounts).
	Placement *Placement
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
		return out, blocked(blockedby.HumanDecision, "%s is a grilling decision: the human resolves it, one per session; run /rota-brainstorm %s", id, id)
	}
	kind, kindSource, placement, err := e.placeWorker(ctx, root, in, out)
	if err != nil {
		return out, err
	}
	hz, err := worker.LaunchHarness(kind)
	if err != nil {
		return out, err
	}
	kind = hz.Kind()
	out.Kind, out.KindSource, out.Placement = kind, kindSource, placement
	model := resolveModel(in.FlagModel, out.Pick.Model, set.Model(kind, in.Tier))
	out.Model, out.AppliedModel = model, model
	if !e.Worker.ModelAppliesTo(root, kind) {
		// A model asked for by name must reach the launch line; the tier map's
		// is a default and only warns.
		if in.FlagModel != "" || out.Pick.Model != "" {
			src := labels.ModelPrefix + out.Pick.Model + " label"
			if in.FlagModel != "" {
				src = "--model " + in.FlagModel
			}
			return out, blocked(blockedby.ModelLabel, "%s cannot apply: %s has no {model} placeholder", src, hz.CommandKey())
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
			return out, blocked(blockedby.OutOfScope, "%s is outside the round's scope (%s)", id, scope)
		}
	}
	return out, nil
}

// placeWorker settles the worker's harness and account. An issue or call that
// names a harness (--kind, a harness: label), a best-of pair's fixed kind, a
// transfer's receiver and a slot resuming its issue pin the kind; the account
// is still picked inside it. Otherwise every harness the round may use
// competes by headroom (pickWorker), narrowed to the one whose round.tiers
// names the model when the issue or call names one. A project with no
// round.harnesses and no round.accounts has nothing to balance: the slot's
// kind, else claude, and the orchestrator's own login.
func (e Env) placeWorker(ctx context.Context, root string, in preflightIn, out preflightOut) (kind, source string, pl *Placement, err error) {
	set := in.Settings
	cfg := e.Worker.Config(root)
	kind, source = resolveKind(in.FlagKind, out.Pick.Harness, "", in.SlotKind, in.Resuming)
	pinned := source == KindFromFlag || source == KindFromLabel || source == KindFromSlotResume
	if in.Fixed != nil {
		kind, source, pinned = in.Fixed.kind, in.Fixed.kindSource, true
	}
	if in.NoFallback && !pinned {
		// A transfer keeps the receiver's recorded kind; the first
		// round.harnesses entry stands in only for a receiver with none.
		if in.SlotKind == "" && set.DefaultKind() != "" {
			kind, source = set.DefaultKind(), KindFromHarnesses
		}
		pinned = true
	}
	if _, err := worker.LaunchHarness(kind); err != nil {
		return kind, source, nil, err
	}
	// A resuming slot keeps its kind and account (dispatch refuses one at its
	// limit), and solo runs every subagent on the orchestrator's own account.
	if in.Resuming || worker.IsSolo(root) || !poolConfigured(cfg, set) {
		return kind, source, nil, e.checkTierModel(root, kind, in, out)
	}
	kinds, restriction := []string{kind}, kind
	if !pinned {
		kinds, restriction = allowedKinds(cfg, set), "any harness"
		if name := strutil.FirstNonEmpty(in.FlagModel, out.Pick.Model); name != "" {
			if kinds = kindsOfModel(set, kinds, name); len(kinds) == 0 {
				return kind, source, nil, blocked(blockedby.ModelLabel, "model %s is named by no round.tiers entry of the harnesses this round may use", name)
			}
			restriction = "model " + name
		}
	}
	// A kind with no model for the tier cannot take the worker.
	var usable []string
	var refused error
	for _, k := range kinds {
		if cerr := e.checkTierModel(root, k, in, out); cerr != nil {
			refused = cerr
			continue
		}
		usable = append(usable, k)
	}
	if len(usable) == 0 {
		return kind, source, nil, refused
	}
	if pinned {
		restriction = fmt.Sprintf("the %s harness", kind)
	}
	pi := workerPickIn{Kinds: usable, Set: set, Slot: in.Slot, SlotKind: in.SlotKind, SlotAccount: in.SlotAccount, Margin: set.AccountRebalanceMargin,
		Keep: in.NoFallback || in.Resuming, Tier: in.Tier, FlagModel: in.FlagModel, LabelModel: out.Pick.Model, Restriction: restriction}
	p, err := e.pickWorker(ctx, root, pi)
	if err != nil {
		return kind, source, nil, err
	}
	if !pinned {
		source = KindFromUsage
		if len(allowedKinds(cfg, set)) == 1 {
			_, source = resolveKind("", "", p.Kind, in.SlotKind, false)
		}
	}
	return p.Kind, source, &p, nil
}

// checkTierModel refuses a kind whose worker would start with no model: an
// unset codex tier map is allowed (the default codex command drops --model and
// Codex picks its own), but a custom command holding {model} would fail at
// dispatch, after the claim.
func (e Env) checkTierModel(root, kind string, in preflightIn, out preflightOut) error {
	if resolveModel(in.FlagModel, out.Pick.Model, in.Settings.Model(kind, in.Tier)) == "" && e.Worker.NeedsModel(root, kind) {
		return blocked(blockedby.NoTierMap, "round.tiers.%s has no model for the %s tier: set round.tiers.%s.*", kind, in.Tier, kind)
	}
	return nil
}

// kindsOfModel narrows kinds to the ones whose round.tiers names the model;
// rota has no other model-to-harness map. None naming it is empty.
func kindsOfModel(set roundcfg.Settings, kinds []string, model string) []string {
	var out []string
	for _, k := range kinds {
		for _, m := range set.Models[k] {
			if m == model {
				out = append(out, k)
				break
			}
		}
	}
	return out
}
