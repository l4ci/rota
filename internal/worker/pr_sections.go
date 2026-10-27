package worker

import (
	"fmt"
	"strings"
)

const (
	GateAssumptionsMissing = "assumptions-missing"
	GateRulingsMissing     = "rulings-missing"
)

// requiredPRSection checks headings only. Empty sections and None both pass;
// the reviewer, rather than this mechanical check, judges their content.
func requiredPRSection(body string) (verdict, heading string) {
	found := map[string]bool{}
	for _, line := range splitLines(body) {
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
	g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef)
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
