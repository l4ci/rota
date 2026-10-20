package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// waitingPR is a PR that waits for a merge: a done slot's PR or one parked in
// the review queue. Reviewed is true when a review verdict is recorded at its
// head, the state the train needs from every member.
type waitingPR struct {
	PR, Base string
	Reviewed bool
}

// trainNudge is a base with two or more reviewed PRs waiting: one train lands
// them in a single verify where a gate per PR would run one full verify each.
type trainNudge struct {
	Base string
	PRs  []string
}

// Command is the train to run, the PR numbers as `rota worker train` takes them.
func (n trainNudge) Command() string {
	base := n.Base
	if base == "" {
		base = "<base>"
	}
	return fmt.Sprintf("rota worker train %s --base %s", strings.Join(n.PRs, " "), base)
}

// Text is the nudge line `round wait` and `round watch` print.
func (n trainNudge) Text() string {
	return fmt.Sprintf("nudge\ttrain\t%d reviewed PRs wait on %s (%s)\t%s",
		len(n.PRs), dash(n.Base), strings.Join(prHashes(n.PRs), " "), n.Command())
}

func prHashes(prs []string) []string {
	out := make([]string, len(prs))
	for i, p := range prs {
		out[i] = "#" + p
	}
	return out
}

// trainNudges groups the reviewed waiting PRs by base. A base with one stays
// out: a lone PR is a plain gate.
func trainNudges(waiting []waitingPR) []trainNudge {
	by := map[string][]string{}
	var bases []string
	for _, w := range waiting {
		if !w.Reviewed {
			continue
		}
		n, ok := worker.PRRefNumber(w.PR)
		if !ok {
			continue
		}
		if _, seen := by[w.Base]; !seen {
			bases = append(bases, w.Base)
		}
		by[w.Base] = append(by[w.Base], fmt.Sprint(n))
	}
	var out []trainNudge
	for _, b := range bases {
		if prs := by[b]; len(prs) >= 2 {
			sort.Slice(prs, func(i, j int) bool { return len(prs[i]) < len(prs[j]) || len(prs[i]) == len(prs[j]) && prs[i] < prs[j] })
			out = append(out, trainNudge{Base: b, PRs: prs})
		}
	}
	return out
}

// waitingPRs reads the registry for the PRs that wait for a merge. Tolerant: a
// nudge is advice, so an unreadable registry or branch reads as nothing waiting.
func waitingPRs(c *Ctx, root string) []waitingPR {
	reg := worker.LoadRegistryTolerant(root)
	reviewed := func(branch string) bool {
		if branch == "" {
			return false
		}
		r, err := c.deps().Git(c.Context(), root, "rev-parse", "--verify", "-q", branch)
		if err != nil || r.ExitCode != 0 {
			return false
		}
		return len(verdict.Recorded(root, branch, strings.TrimSpace(r.Stdout))) > 0
	}
	var out []waitingPR
	for _, s := range reg.Slots() {
		if s.PR() != "" && strings.EqualFold(s.State(), "done") {
			out = append(out, waitingPR{PR: s.PR(), Base: s.Base(), Reviewed: reviewed(s.Branch())})
		}
	}
	for _, q := range reg.PRs() {
		if q.PR != "" {
			out = append(out, waitingPR{PR: q.PR, Base: q.Base, Reviewed: reviewed(q.Branch)})
		}
	}
	return out
}

// trainNudgeData is the nudges as the `trainNudge` JSON array, nil when none.
func trainNudgeData(ns []trainNudge) []any {
	if len(ns) == 0 {
		return nil
	}
	rows := make([]any, 0, len(ns))
	for _, n := range ns {
		o := jsonx.NewObject()
		o.Set("base", n.Base)
		o.Set("prs", strList(n.PRs))
		o.Set("command", n.Command())
		rows = append(rows, o)
	}
	return rows
}

// trainNudgeLines is the nudges as text lines.
func trainNudgeLines(ns []trainNudge) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Text())
	}
	return out
}

// tickNeedsMerge is true when the tick left PRs for a person to merge
// (ship.mergeApproval is not none), so the train is the orchestrator's to run.
func tickNeedsMerge(r roundtick.Result) bool {
	for _, it := range r.NeedsYou {
		if it.Kind == "merge" {
			return true
		}
	}
	return false
}
