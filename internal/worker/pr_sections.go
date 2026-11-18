package worker

import (
	"fmt"
	"github.com/l4ci/rota/internal/landing"
	"strings"

	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
)

// requiredPRSection checks headings only. Empty sections and None both pass;
// the reviewer, rather than this mechanical check, judges their content.
func requiredPRSection(body string) (verdict, headings string) {
	found := prSectionHeadings(body)
	var missing []string
	for _, section := range []struct{ verdict, heading string }{
		{landing.GateAssumptionsMissing, "## Assumptions"},
		{landing.GateRulingsMissing, "## Rulings"},
	} {
		if !found[strings.ToLower(section.heading)] {
			if verdict == "" {
				verdict = section.verdict
			}
			missing = append(missing, section.heading)
		}
	}
	return verdict, strings.Join(missing, " and ")
}

func prSectionHeadings(body string) map[string]bool {
	found := map[string]bool{}
	for _, line := range pystr.Splitlines(body) {
		found[strings.ToLower(strings.TrimSpace(line))] = true
	}
	return found
}

// checkRequiredPRSections uses the same successful PR read as provenance.
// A gate without a remote PR, or a missing forge CLI, has no body to check.
func (g *gateRun) checkRequiredPRSections() (bool, error) {
	v, msg, hint := g.requiredSectionsGap()
	if v == "" {
		return false, nil
	}
	g.verdict(v, msg, hint)
	return true, nil
}

// requiredSectionsGap is the refusal for a PR body missing a required section,
// "" when it has them or there is no body to read.
func (g *gateRun) requiredSectionsGap() (verdict, msg, hint string) {
	if g.sectionBody == nil {
		return "", "", ""
	}
	verdict, heading := requiredPRSection(*g.sectionBody)
	if verdict == "" {
		return "", "", ""
	}
	if g.headRef != "" {
		g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef)
	}
	return verdict, fmt.Sprintf("GATE %s refused — PR %s is missing %s; nothing landed", g.o.Slot, g.pr, heading),
		fmt.Sprintf("add %s to the PR body (None is allowed), then retry", heading)
}

func missingPRSection(verdict string) bool {
	return verdict == landing.GateAssumptionsMissing || verdict == landing.GateRulingsMissing
}

func excludeTrainMember(res *landing.TrainResult, target string, gr landing.GateResult) bool {
	if !missingPRSection(gr.Verdict) && gr.Verdict != landing.GateReviewMissing {
		return false
	}
	skipTrainMember(res, target, gr)
	return true
}

func skipTrainMember(res *landing.TrainResult, target string, gr landing.GateResult) {
	res.Culprits = append(res.Culprits, landing.TrainCulprit{Target: target, PR: gr.PR, BlockedBy: gr.Verdict, Gate: gr})
	res.Notes = append(res.Notes, fmt.Sprintf("TRAIN-SKIP %s (%s) — blockedBy: %s; %s", target, gr.PR, gr.Verdict, gr.Err))
}

// filterPRSections reads every PR body before the ordinary member checks.
// It does not fetch, merge, run provenance/review checks, or verify code.
func (t *trainRun) filterPRSections(res *landing.TrainResult, in gateInput, reg Registry, targets []string) ([]string, bool, error) {
	var kept []string
	for _, target := range targets {
		gt, err := reg.GateTargetAny(target)
		if err != nil {
			return nil, true, err
		}
		gr, err := t.prSectionGate(in, target, gt)
		res.Notes = appendNew(res.Notes, gr.Notes...)
		if err != nil {
			return nil, true, err
		}
		if missingPRSection(gr.Verdict) {
			// The prefilter decides the skip; the full check only widens its gap list.
			gr, err = t.fullGaps(in, target, gr)
			if err != nil {
				return nil, true, err
			}
			skipTrainMember(res, target, gr)
			continue
		}
		if excludeTrainMember(res, target, gr) {
			continue
		}
		if !gr.OK() {
			res.Verdict, res.Culprit, res.CulpritGate, res.Err, res.Hint = gr.Verdict, target, gr, gr.Err, gr.Hint
			return nil, true, nil
		}
		kept = append(kept, target)
	}
	return kept, false, nil
}

// fullGaps runs the member's ordinary check so a section-dropped member lists
// every record gap, in the order `rota worker gate` does. It keeps the
// prefilter's result when the check found no further gap.
func (t *trainRun) fullGaps(in gateInput, target string, dropped landing.GateResult) (landing.GateResult, error) {
	full, err := t.e.Gate(t.ctx, t.root, t.memberCheckOpts(target, in))
	if err != nil && full.Verdict != landing.GateVerdictBlocked {
		return dropped, err
	}
	if len(full.Gaps) < 2 {
		return dropped, nil
	}
	full.Notes = appendNew(dropped.Notes, full.Notes...)
	return full, nil
}

func (t *trainRun) prSectionGate(in gateInput, target string, gt GateTarget) (landing.GateResult, error) {
	gr := landing.GateResult{Slot: target, Base: t.o.Base, TargetSlot: gt.Name, Issue: gt.Issue}
	g := gateRun{e: t.e, ctx: t.ctx, root: t.root, in: in, resolved: gt, o: GateOpts{Slot: target, Base: t.o.Base, CheckOnly: true, NoVerdict: true}}
	first := landingStageResult[struct{}, landing.GateResult]{Result: gr}
	forged := runGateStage(first, func(result landing.GateResult) (gateForgeResult, landing.GateResult, bool, error) {
		return g.stepForge(result, gt)
	})
	adopted := runGateStage(forged, func(result landing.GateResult) (GateTarget, landing.GateResult, bool, error) {
		return g.stepAdoptPR(result, gt, forged.Value)
	})
	remote := runGateStage(adopted, func(result landing.GateResult) (bool, landing.GateResult, bool, error) {
		return g.stepRemote(result, adopted.Value.PR)
	})
	if remote.Done || remote.Err != nil {
		return remote.Result, remote.Err
	}
	state := g.operation(remote.Result, adopted.Value, forged.Value)
	gr = *state.res
	state.res = &gr
	gr.Verdict = landing.GateFresh
	if !remote.Value {
		return gr, nil
	}
	info, err := state.prView()
	if err != nil {
		if tracker.IsCLIMissing(err) {
			return gr, nil
		}
		_, err = state.broke(fmt.Sprintf("could not read the body of %s to check required PR sections: %v", state.pr, err))
		return gr, err
	}
	if info.State != "OPEN" {
		return gr, nil
	}
	gr.Branch, gr.SHA = info.Head, info.HeadSHA
	if gt.External {
		gr.Issue = HeldID(gt.Task, info.Head, gt.Name)
	}
	state.sectionBody = &info.Body
	_, err = state.checkRequiredPRSections()
	return gr, err
}

// Validate --order against the original membership, then remove the excluded
// culprits and already-merged PRs. Invalid or duplicate tokens still fail.
func filterSectionOrder(res landing.TrainResult, o *TrainOpts, merged []landing.TrainMember) error {
	if (len(res.Culprits) == 0 && len(merged) == 0) || len(o.Order) == 0 {
		return nil
	}
	all := append([]landing.TrainMember{}, res.Members...)
	for _, c := range res.Culprits {
		all = append(all, landing.TrainMember{Target: c.Target, Branch: c.Gate.Branch, PR: c.PR})
	}
	all = append(all, merged...)
	perm, _, _, err := orderTrain(all, make([]trainFoot, len(all)), o.Order)
	if err != nil {
		return err
	}
	o.Order = nil
	for _, i := range perm {
		if i < len(res.Members) {
			o.Order = append(o.Order, all[i].Target)
		}
	}
	return nil
}
