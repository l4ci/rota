package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundtick"
	"github.com/l4ci/rota/internal/stamp"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

// waitingPR is a PR that waits for a merge: a done slot's PR or one parked in
// the review queue. Reviewed is true when a review verdict is recorded at its
// head and none blocks the gate, the state the train needs from every member.
type waitingPR struct {
	PR, Base   string
	Reviewed   bool
	ReadySince time.Time
	Immediate  bool
}

// trainNudge is a base whose reviewed PRs are due for landing: a train, or a
// lone gate when the configured fill deadline expires.
type trainNudge struct {
	Base       string
	PRs        []string
	Assign     *assignNudge
	Reason     string
	Wait       string
	ReadyCount int
}

// Command is the train to run, the PR numbers as `rota worker train` takes them.
func (n trainNudge) Command() string {
	base := n.Base
	if base == "" {
		base = "<base>"
	}
	if len(n.PRs) == 1 {
		return fmt.Sprintf("rota worker gate %s --base %s", n.PRs[0], base)
	}
	return fmt.Sprintf("rota worker train %s --base %s", strings.Join(n.PRs, " "), base)
}

// Text is the nudge line `round wait` and `round watch` print.
func (n trainNudge) Text() string {
	command := n.Command()
	if n.Assign != nil {
		command = n.Assign.First() + "; then " + command
	}
	reason := ""
	if n.Reason != "" {
		reason = "; " + n.Reason
		if n.Wait != "" {
			reason += "; " + n.Wait
		}
	}
	if n.Reason != "" {
		count, noun, verb := fmt.Sprint(len(n.PRs)), "ready PRs", "wait"
		if len(n.PRs) == 1 {
			noun, verb = "ready PR", "waits"
		}
		if n.ReadyCount > len(n.PRs) {
			count = fmt.Sprintf("%d of %d", len(n.PRs), n.ReadyCount)
			noun = "ready PRs"
		}
		return fmt.Sprintf("nudge\ttrain\t%s %s %s on %s (%s)%s\t%s", count, noun, verb, dash(n.Base), strings.Join(prHashes(n.PRs), " "), reason, command)
	}
	return fmt.Sprintf("nudge\ttrain\t%d reviewed PRs wait on %s (%s)%s\t%s",
		len(n.PRs), dash(n.Base), strings.Join(prHashes(n.PRs), " "), reason, command)
}

func readyTrainNudges(c *Ctx, root string) []trainNudge {
	// An invalid train key is refused as the tick refuses it; an unrelated
	// invalid round key must not hide legacy nudges, so read only these keys.
	fill, err := roundcfg.TrainFillFromConfig(c.deps().Config(root))
	if err != nil {
		return nil
	}
	ns := trainNudgesWithFill(waitingPRs(c, root), fill, c.deps().Now())
	if len(ns) > 0 {
		n := freeSlotAssignment(c, root)
		for i := range ns {
			ns[i].Assign = n
		}
	}
	return ns
}

// trainHoldingLines reads current progress without consuming changed-only
// notices or running a tick. Status can show elapsed time while watch sleeps.
func trainHoldingLines(c *Ctx, root string) []string {
	fill, err := roundcfg.TrainFillFromConfig(c.deps().Config(root))
	if err != nil || !fill.Enabled() {
		return nil
	}
	type group struct {
		count  int
		oldest time.Time
	}
	groups := map[string]group{}
	var bases []string
	seen := map[int]bool{}
	for _, w := range waitingPRs(c, root) {
		n, parsed := worker.PRRefNumber(w.PR)
		if !w.Reviewed || w.Immediate || !parsed || seen[n] {
			continue
		}
		seen[n] = true
		g, known := groups[w.Base]
		if !known {
			bases = append(bases, w.Base)
		}
		g.count++
		if !w.ReadySince.IsZero() && (g.oldest.IsZero() || w.ReadySince.Before(g.oldest)) {
			g.oldest = w.ReadySince
		}
		groups[w.Base] = g
	}
	now := c.deps().Now()
	var lines []string
	for _, base := range bases {
		g := groups[base]
		if fill.Due(g.count, g.oldest, now) == "" {
			lines = append(lines, fill.Holding(base, g.count, g.oldest, now))
		}
	}
	return lines
}

func prHashes(prs []string) []string {
	out := make([]string, len(prs))
	for i, p := range prs {
		out[i] = "#" + p
	}
	return out
}

// trainNudgesWithFill groups reviewed PRs by base and proposes due groups.
// The zero policy preserves the legacy nudge at two reviewed PRs.
func trainNudgesWithFill(waiting []waitingPR, fill roundcfg.TrainFill, now time.Time) []trainNudge {
	by := map[string][]string{}
	oldest := map[string]time.Time{}
	immediate := map[string][]string{}
	ages := map[string]time.Time{}
	seen := map[int]bool{}
	var bases []string
	for _, w := range waiting {
		if !w.Reviewed {
			continue
		}
		n, ok := worker.PRRefNumber(w.PR)
		if !ok || seen[n] {
			continue
		}
		seen[n] = true
		if _, known := by[w.Base]; !known {
			bases = append(bases, w.Base)
		}
		by[w.Base] = append(by[w.Base], fmt.Sprint(n))
		ages[fmt.Sprint(n)] = w.ReadySince
		if w.Immediate {
			immediate[w.Base] = append(immediate[w.Base], fmt.Sprint(n))
		}
		if at := w.ReadySince; !at.IsZero() && (oldest[w.Base].IsZero() || at.Before(oldest[w.Base])) {
			oldest[w.Base] = at
		}
	}
	var out []trainNudge
	for _, b := range bases {
		prs := by[b]
		count := len(prs)
		observedSince := oldest[b]
		reason := fill.Due(len(prs), oldest[b], now)
		if fill.Enabled() && reason == "" && len(immediate[b]) > 0 {
			reason = "review disabled; ready time unavailable, land now"
			prs = immediate[b]
			observedSince = time.Time{}
		}
		if fill.Enabled() && reason != "" || !fill.Enabled() && len(prs) >= 2 {
			wait := ""
			if fill.Enabled() {
				wait = fill.Wait(observedSince, now)
			}
			sort.Slice(prs, func(i, j int) bool { return len(prs[i]) < len(prs[j]) || len(prs[i]) == len(prs[j]) && prs[i] < prs[j] })
			if fill.Max > 0 && len(prs) > fill.Max {
				prs = roundcfg.OldestReady(prs, fill.Max, func(pr string) time.Time { return ages[pr] })
			}
			out = append(out, trainNudge{Base: b, PRs: prs, Reason: reason, Wait: wait, ReadyCount: count})
		}
	}
	return out
}

// waitingPRs reads the registry for the PRs that wait for a merge. Tolerant: a
// nudge is advice, so an unreadable registry or branch reads as nothing waiting.
func waitingPRs(c *Ctx, root string) []waitingPR {
	reg := worker.LoadRegistryTolerant(root)
	state := roundtick.State{}
	if round, ok := reg.Round(); ok {
		if cd, err := rotastate.CommonDirVia(c.Context(), c.deps().Git, root); err == nil {
			state = roundtick.LoadState(cd, round)
		}
	}
	return waitingPRsWithState(c, root, state)
}

func waitingPRsWithState(c *Ctx, root string, state roundtick.State) []waitingPR {
	reg := worker.LoadRegistryTolerant(root)
	cfg := c.deps().Config(root)
	policy, _ := config.ReviewPolicyOf(cfg)
	fill, _ := roundcfg.TrainFillFromConfig(cfg) // callers refuse a bad value before this
	reviewOff := fill.Enabled() && policy.Default == config.DepthNone
	store, _ := c.deps().workerEnv().BounceStore(root)
	bounceHead := func(issue string) string {
		if store == nil || issue == "" {
			return ""
		}
		_, head, _ := store.Count(issue)
		return head
	}
	vc := shipVerdict(c, root, root)
	reviewed := func(branch, issue, target, pr string, doneAt time.Time) (bool, time.Time) {
		if branch == "" {
			return false, time.Time{}
		}
		r, err := c.deps().Git(c.Context(), root, "rev-parse", "--verify", "-q", branch)
		if err != nil || r.ExitCode != 0 {
			return false, time.Time{}
		}
		head := strings.TrimSpace(r.Stdout)
		if n, ok := worker.PRRefNumber(pr); ok {
			for _, name := range []string{target, pr, fmt.Sprintf("#%d", n), fmt.Sprint(n)} {
				if _, held := roundtick.HoldReason(state.Held, state.FailedHeads, name, fmt.Sprintf("%d:%s", n, head)); held {
					return false, time.Time{}
				}
			}
		}
		if bh := bounceHead(issue); bh != "" && head != "" && (strings.HasPrefix(head, bh) || strings.HasPrefix(bh, head)) {
			return false, time.Time{} // sent back and not yet pushed to
		}
		if reviewOff {
			return !trainBlocked(vc.Store, branch), doneAt
		}
		ready := len(verdict.Recorded(root, branch, head)) > 0 && !trainBlocked(vc.Store, branch)
		return ready, reviewReadySince(vc.Store.Branches[verdict.BranchKey("", branch)], head)
	}
	var out []waitingPR
	for _, s := range reg.Slots() {
		if s.PR() != "" && worker.IsDone(s.State()) {
			at, _ := stamp.Parse(s.ActiveAt())
			if event := s.Event(); event != nil && (event.Kind == "done" || event.Kind == "review-reply") {
				if doneAt, err := stamp.Parse(event.TS); err == nil {
					at = doneAt
				}
			}
			ready, since := reviewed(s.Branch(), s.Issue(), s.Name(), s.PR(), at)
			out = append(out, waitingPR{PR: s.PR(), Base: s.Base(), Reviewed: ready, ReadySince: since, Immediate: reviewOff && ready && since.IsZero()})
		}
	}
	for _, q := range reg.PRs() {
		if q.PR != "" {
			ready, since := reviewed(q.Branch, q.Issue, q.From, q.PR, time.Time{})
			out = append(out, waitingPR{PR: q.PR, Base: q.Base, Reviewed: ready, ReadySince: since, Immediate: reviewOff && ready && since.IsZero()})
		}
	}
	return fillDefaultBase(openOnly(c, root, out), defaultBase(c, root))
}

// reviewReadySince uses the last current-head review prerequisite, so an old
// PR or a review of a previous head cannot make a fresh review expire early.
func reviewReadySince(records []verdict.Record, head string) time.Time {
	var since time.Time
	list := verdict.ForHead(records, head)
	for _, kind := range append([]string{verdict.ReviewSpec, verdict.ReviewQuality, verdict.ReviewCross, verdict.SecondOpinion}, verdict.LensKinds...) {
		r, ok := verdict.Latest(list, kind)
		if !ok || !verdict.AtHead(r, head) {
			continue
		}
		if at, err := stamp.Parse(r.RecordedAt); err == nil && at.After(since) {
			since = at
		}
	}
	return since
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
func trainBlocked(store verdict.Store, branch string) bool {
	list := store.Branches[verdict.BranchKey("", branch)]
	_, blocked := verdict.BlockingAnyHead(list)
	return blocked
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
		setIf(o, "reason", n.Reason)
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
// nudge lines appended, when reviewed PRs on one base are due. Shared
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
