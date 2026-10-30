package worker

import (
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
)

const (
	GateAssumptionsMissing = "assumptions-missing"
	GateRulingsMissing     = "rulings-missing"
)

// requiredPRSection checks headings only. Empty sections and None both pass;
// the reviewer, rather than this mechanical check, judges their content.
func requiredPRSection(body string) (verdict, heading string) {
	found := map[string]bool{}
	for _, line := range pystr.Splitlines(body) {
		found[strings.ToLower(strings.TrimSpace(line))] = true
	}
	for _, section := range []struct{ verdict, heading string }{
		{GateAssumptionsMissing, "## Assumptions"},
		{GateRulingsMissing, "## Rulings"},
	} {
		if !found[strings.ToLower(section.heading)] {
			return section.verdict, section.heading
		}
	}
	return "", ""
}

// checkRequiredPRSections uses the same successful PR read as provenance.
// A gate without a remote PR, or a missing forge CLI, has no body to check.
func (g *gate) checkRequiredPRSections() (bool, error) {
	if g.sectionBody == nil {
		return false, nil
	}
	verdict, heading := requiredPRSection(*g.sectionBody)
	if verdict == "" {
		return false, nil
	}
	if g.headRef != "" {
		g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef)
	}
	g.verdict(verdict, fmt.Sprintf("GATE %s refused — PR %s is missing %s; nothing landed", g.o.Slot, g.pr, heading),
		fmt.Sprintf("add %s to the PR body (None is allowed), then retry", heading))
	return true, nil
}

func missingPRSection(verdict string) bool {
	return verdict == GateAssumptionsMissing || verdict == GateRulingsMissing
}

// TrainCulprit is a member excluded by the static PR-section check before the
// train builds or verifies its merge. The remaining members train normally.
type TrainCulprit struct {
	Target    string
	PR        string
	BlockedBy string
	Gate      GateResult
}

func (t *trainRun) excludePRSections(target string, gr GateResult) bool {
	if !missingPRSection(gr.Verdict) {
		return false
	}
	t.res.Culprits = append(t.res.Culprits, TrainCulprit{Target: target, PR: gr.PR, BlockedBy: gr.Verdict, Gate: gr})
	t.res.Notes = append(t.res.Notes, fmt.Sprintf("TRAIN-SKIP %s (%s) — blockedBy: %s; %s", target, gr.PR, gr.Verdict, gr.Err))
	return true
}

// filterPRSections reads every PR body before the ordinary member checks.
// It does not fetch, merge, run provenance/review checks, or verify code.
func (t *trainRun) filterPRSections(reg Registry, targets []string) ([]string, bool, error) {
	var kept []string
	for _, target := range targets {
		gt, err := reg.GateTargetAny(target)
		if err != nil {
			return nil, true, err
		}
		gr, err := t.prSectionGate(target, gt)
		t.res.Notes = appendNew(t.res.Notes, gr.Notes...)
		if err != nil {
			return nil, true, err
		}
		if t.excludePRSections(target, gr) {
			continue
		}
		if !gr.OK() {
			t.res.Verdict, t.res.Culprit, t.res.CulpritGate, t.res.Err, t.res.Hint = gr.Verdict, target, gr, gr.Err, gr.Hint
			return nil, true, nil
		}
		kept = append(kept, target)
	}
	return kept, false, nil
}

func (t *trainRun) prSectionGate(target string, gt GateTarget) (GateResult, error) {
	gr := GateResult{Slot: target, Base: t.o.Base, TargetSlot: gt.Name, Issue: gt.Issue}
	g := &gate{e: t.e, ctx: t.ctx, root: t.root, res: &gr, in: t.in, target: gt, resolved: gt,
		o: GateOpts{Slot: target, Base: t.o.Base, CheckOnly: true, NoVerdict: true}, branch: gt.Branch, pr: gt.PR}
	for _, step := range []gateStep{(*gate).stepForge, (*gate).stepAdoptPR, (*gate).stepRemote} {
		if done, err := step(g); done || err != nil {
			return gr, err
		}
	}
	gr.Verdict = GateFresh
	if !g.remote {
		return gr, nil
	}
	info, err := g.prView()
	if err != nil {
		if tracker.IsCLIMissing(err) {
			return gr, nil // the regular gate supplies its normal skip note
		}
		_, err = g.broke(fmt.Sprintf("could not read the body of %s to check required PR sections: %v", g.pr, err))
		return gr, err
	}
	if info.State != "OPEN" {
		return gr, nil // the ordinary gate handles closed or already merged PRs
	}
	gr.Branch, gr.SHA = info.Head, info.HeadSHA
	if gt.External {
		gr.Issue = HeldID(gt.Task, info.Head, gt.Name)
	}
	g.sectionBody = &info.Body
	_, err = g.checkRequiredPRSections()
	return gr, err
}

// Validate --order against the original membership, then remove only the
// section culprits. Invalid or duplicate order tokens still fail as before.
func (t *trainRun) filterSectionOrder(o *TrainOpts) error {
	if len(t.res.Culprits) == 0 || len(o.Order) == 0 {
		return nil
	}
	all := append([]TrainMember{}, t.res.Members...)
	for _, c := range t.res.Culprits {
		all = append(all, TrainMember{Target: c.Target, Branch: c.Gate.Branch, PR: c.PR})
	}
	perm, _, _, err := orderTrain(all, make([]trainFoot, len(all)), o.Order)
	if err != nil {
		return err
	}
	o.Order = nil
	for _, i := range perm {
		if i < len(t.res.Members) {
			o.Order = append(o.Order, all[i].Target)
		}
	}
	return nil
}
