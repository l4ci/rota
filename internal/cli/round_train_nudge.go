package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// waitingPR is a PR that waits for a merge: a done slot's PR or one parked in
// the review queue. Reviewed is true when a review verdict is recorded at its
// head and none blocks the gate, the state the train needs from every member.
type waitingPR struct {
	PR, Base string
	Reviewed bool
}

// trainNudge is a base with two or more reviewed PRs waiting: one train lands
// them in a single verify where a gate per PR would run one full verify each.
type trainNudge struct {
	Base   string
	PRs    []string
	Assign *assignNudge
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
	command := n.Command()
	if n.Assign != nil {
		command = n.Assign.First() + "; then " + command
	}
	return fmt.Sprintf("nudge\ttrain\t%d reviewed PRs wait on %s (%s)\t%s",
		len(n.PRs), dash(n.Base), strings.Join(prHashes(n.PRs), " "), command)
}

func readyTrainNudges(c *Ctx, root string) []trainNudge {
	ns := trainNudges(waitingPRs(c, root))
	if len(ns) > 0 {
		n := freeSlotAssignment(c, root)
		for i := range ns {
			ns[i].Assign = n
		}
	}
	return ns
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
	store, _ := c.deps().workerEnv().BounceStore(root)
	bounceHead := func(issue string) string {
		if store == nil || issue == "" {
			return ""
		}
		_, head, _ := store.Count(issue)
		return head
	}
	vc := shipVerdict(c, root, root)
	reviewed := func(branch, issue string) bool {
		if branch == "" {
			return false
		}
		r, err := c.deps().Git(c.Context(), root, "rev-parse", "--verify", "-q", branch)
		if err != nil || r.ExitCode != 0 {
			return false
		}
		head := strings.TrimSpace(r.Stdout)
		if bh := bounceHead(issue); bh != "" && head != "" && (strings.HasPrefix(head, bh) || strings.HasPrefix(bh, head)) {
			return false // sent back and not yet pushed to
		}
		return len(verdict.Recorded(root, branch, head)) > 0 && !trainBlocked(vc.Store, branch, vc.Settings)
	}
	var out []waitingPR
	for _, s := range reg.Slots() {
		if s.PR() != "" && worker.IsDone(s.State()) {
			out = append(out, waitingPR{PR: s.PR(), Base: s.Base(), Reviewed: reviewed(s.Branch(), s.Issue())})
		}
	}
	for _, q := range reg.PRs() {
		if q.PR != "" {
			out = append(out, waitingPR{PR: q.PR, Base: q.Base, Reviewed: reviewed(q.Branch, q.Issue)})
		}
	}
	return fillDefaultBase(openOnly(c, root, out), defaultBase(c, root))
}

// openOnly drops the PRs the forge no longer lists as open: gate and train
// merge a PR without clearing its registry row at once. A forge that cannot
// answer (or a PR number that does not parse) keeps the row, since the nudge is
// advice and a stale name beats a missing one.
func openOnly(c *Ctx, root string, waiting []waitingPR) []waitingPR {
	if len(waiting) == 0 {
		return waiting
	}
	open, ok := c.deps().workerEnv().OpenPRNumbers(c.Context(), root)
	if !ok {
		return waiting
	}
	var out []waitingPR
	for _, w := range waiting {
		if n, parsed := worker.PRRefNumber(w.PR); parsed && !open[n] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// defaultBase is the base a PR with no recorded base merges into, "" when it
// cannot be resolved. Tolerant like the rest of the nudge. resolveBase already
// tries git.baseBranch first, so "" means no base is knowable at all; empty-base
// PRs then stay in their own group, apart from PRs with an explicit base, and
// the nudge may under-report a train. Advice only, so that degrade is accepted.
func defaultBase(c *Ctx, root string) string {
	base, ok, err := resolveBase(c.Context(), c.deps(), root)
	if err != nil || !ok {
		return ""
	}
	return base
}

// fillDefaultBase names the default base on PRs that recorded none, so a done
// slot (empty base) and a queued PR (explicit base) on the same branch group
// together.
func fillDefaultBase(waiting []waitingPR, def string) []waitingPR {
	for i := range waiting {
		if waiting[i].Base == "" {
			waiting[i].Base = def
		}
	}
	return waiting
}

// trainBlocked is true when the branch's recorded verdicts would block the
// gate or bounce the PR: a FAIL (review or second opinion) or a review with a
// blocking finding keeps it out of a nudged train, a clean CONCERNS and PASS
// let it in.
func trainBlocked(store verdict.Store, branch string, s verdict.Settings) bool {
	list := store.Branches[verdict.BranchKey("", branch)]
	if _, blocked := verdict.Blocking(list, s); blocked {
		return true
	}
	if r, ok := verdict.Latest(list, verdict.ReviewCross); ok && r.Failing() {
		return true
	}
	r, ok := verdict.EffectiveReview(list)
	return ok && r.Failing()
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
		if n.Assign != nil {
			a := n.Assign.data()
			a.Set("command", n.Assign.Command()+" --agent "+n.Assign.Slot)
			o.Set("assignFirst", a)
		}
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

// addTrainNudge sets the `trainNudge` array on d and returns text with the
// nudge lines appended, when two or more reviewed PRs wait on one base. Shared
// by `round watch` and `round tick` (`round wait` builds its own): a tick that merged nothing
// still leaves the nudge, since PRs the autopilot held or has not reached wait.
func addTrainNudge(c *Ctx, root string, d *jsonx.Object, text []string) []string {
	nudges := readyTrainNudges(c, root)
	rows := trainNudgeData(nudges)
	if rows == nil {
		return text
	}
	d.Set("trainNudge", rows)
	return append(text, trainNudgeLines(nudges)...)
}
