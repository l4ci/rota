package worker

import (
	"fmt"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/tracker"
	"slices"
	"strconv"
	"strings"
)

// stepForge picks the provider and opens its forge.
func (runner gateRun) stepForge(result GateResult, target GateTarget) (gateForgeResult, GateResult, bool, error) {
	g := runner.operation(result, target, gateForgeResult{})
	finish := func(done bool, err error) (gateForgeResult, GateResult, bool, error) {
		return gateForgeResult{provider: g.provider, forge: g.forge, issues: g.issues, local: g.local, cliName: g.cliName, closedLabels: g.closedLabels}, *g.res, done, err
	}

	if err := g.in.cfg.RemoteErr; err != nil {
		return finish(true, fail(exitcode.ExitRefused, err.Error()))
	}
	if err := g.in.cfg.sourceErr; err != nil {
		return finish(true, fail(exitcode.ExitInternal, err.Error()))
	}
	g.res.Branch, g.res.PR = g.target.Branch, g.target.PR
	var refusal *forgeOpenRefusal
	g.forge, g.provider, refusal = g.e.openForge(g.ctx, g.root, g.pr, g.in.cfg, tracker.ForgeActor)
	if refusal != nil && refusal.reason == forgeOpenProvider {
		g.verdict(GateForge, fmt.Sprintf("GATE %s refused — %v; nothing landed", g.o.Slot, refusal.err),
			"set forge.provider to the forge that holds the PR, or to local to merge directly")
		return finish(true, nil)
	}
	g.cliName = tracker.CLIName(g.provider)
	g.closedLabels = g.in.cfg.ClosedLabels
	if g.provider == tracker.ProviderLocal {
		// No forge: no PR to look for, read or merge. The gate merges directly.
		g.local = true
		if g.pr != "" {
			return finish(g.broke(fmt.Sprintf("slot %s records PR %s, but the forge is local (forge.provider is local, or auto with no origin remote); set forge.provider to the forge that holds the PR", g.o.Slot, g.pr)))
		}
		return finish(false, nil)
	}
	if refusal != nil {
		return finish(g.broke(fmt.Sprintf("cannot reach the %s forge: %v", g.provider, refusal.err)))
	}
	var err error
	if g.issues, err = g.e.issueSide(g.forge, g.provider, g.root, g.in.cfg.Tracker); err != nil {
		return finish(g.broke(fmt.Sprintf("cannot reach the issue source: %v", err)))
	}
	return finish(false, nil)
}

// stepExternal reads the head branch of a PR no slot owns from the forge, so
// the later steps judge it like a slot's: freshness, identity, provenance,
// approval, verify and land. Its issue, for the closes check, is the one its
// branch name leads with (HeldID), else the first its body closes.
func (runner gateRun) stepExternal(result GateResult, target GateTarget, forged gateForgeResult) (GateTarget, GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	finish := func(done bool, err error) (GateTarget, GateResult, bool, error) {
		return g.target, *g.res, done, err
	}

	if !g.target.External || g.pr == "" { // an adopted slot may record no PR yet
		return finish(false, nil)
	}
	if g.local {
		return finish(g.broke(fmt.Sprintf("cannot gate PR %s: the forge is local (forge.provider is local, or auto with no origin remote)", g.pr)))
	}
	g.prNum = prNumText(g.pr)
	info, ok := g.prInfo()
	if !ok {
		return finish(g.broke(fmt.Sprintf("could not read PR %s from %s", g.pr, g.provider)))
	}
	g.branch, g.target.Branch, g.res.Branch = info.Head, info.Head, info.Head
	// the head branch names the issue of a PR no slot or record owns; a branch
	// that names none falls back to the first issue the PR body closes
	g.target.Issue = HeldID(g.target.Task, info.Head, g.target.Name)
	if g.target.Issue == "" {
		if closed := g.forge.ClosedNumbers(info.Body); len(closed) > 0 {
			g.target.Issue = strconv.Itoa(closed[0])
		}
	}
	g.target.Bundle = nil // a bundle belongs to a slot's lead, not to a branch-derived issue
	g.res.Issue = g.target.Issue
	g.target.relays = append(slices.Clone(g.target.relays), g.unrecordedSlotRelays(info.Head)...)
	return finish(false, nil)
}

// unrecordedSlotRelays is the relay log of the slot on the PR's head branch
// that never recorded a PR (the exact branch, never the issue number, which a
// sibling or another agent's branch shares): the worker opened it
// without `worker done` recording it, so the PR resolved as external and would
// otherwise be judged against no relays (bug-16).
func (g *gateRun) unrecordedSlotRelays(head string) []any {
	issue := g.target.Issue
	if issue == "" {
		return nil
	}
	var out []any
	for _, s := range LoadRegistryTolerant(g.root).Slots() {
		if s.PR() == "" && s.Branch() == head {
			out = append(out, s.Relays()...)
		}
	}
	return out
}

// stepAdoptPR looks a PR up by head when the slot recorded none. A worker can
// open its PR without the slot recording it. A local merge would then land on
// the local base only, leave the PR open and still report a pass, so look the
// PR up by head before taking that path.
func (runner gateRun) stepAdoptPR(result GateResult, target GateTarget, forged gateForgeResult) (GateTarget, GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	finish := func(done bool, err error) (GateTarget, GateResult, bool, error) {
		return g.target, *g.res, done, err
	}

	if g.local || g.pr != "" || g.target.Queued || g.branch == "" {
		return finish(false, nil)
	}
	adopted, msg := g.openPRForBranch()
	if msg != "" {
		return finish(g.broke(msg))
	}
	if adopted == "" {
		return finish(false, nil)
	}
	g.pr, g.target.PR, g.res.PR = adopted, adopted, adopted
	g.res.Notes = append(g.res.Notes, fmt.Sprintf("PR-ADOPTED %s — the slot recorded no PR; open PR %s is headed by %s", g.o.Slot, adopted, g.branch))
	if !g.o.CheckOnly {
		UpdateSlot(g.root, g.o.Slot, func(s *Slot) { s.SetPR(adopted) })
	}
	return finish(false, nil)
}

// stepSlotHead follows a slot's PR to its head branch when the two disagree.
// An issue split into stacked PRs ends with a second PR from a new branch, and
// the slot still records the first (merged) one. The head is taken only when
// it leads with the issue the slot holds, so a PR of another issue's branch
// stays a mismatch. A real gate records it on the slot, so the next one starts
// from the right branch.
func (runner gateRun) stepSlotHead(result GateResult, target GateTarget, forged gateForgeResult, remote bool, viewed *tracker.PRInfo) (gateSlotHeadResult, GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.remote = remote
	g.viewed = viewed
	finish := func(done bool, err error) (gateSlotHeadResult, GateResult, bool, error) {
		return gateSlotHeadResult{target: g.target, viewed: g.viewed}, *g.res, done, err
	}

	t := g.target
	if t.External || t.Queued || !g.remote || g.pr == "" || g.branch == "" {
		return finish(false, nil)
	}
	if g.viewed == nil {
		info, ok := g.prInfo()
		if !ok {
			return finish(false, nil)
		}
		g.viewed = &info
	}
	info := *g.viewed // stepPRMatches reads the same PR next: one forge call
	if info.State != "OPEN" || info.Head == "" || info.Head == g.branch {
		return finish(false, nil)
	}
	if held := HeldID(t.Task, g.branch, t.Name); held == "" || HeldID("", info.Head, t.Name) != held {
		return finish(false, nil)
	}
	g.res.Notes = append(g.res.Notes, fmt.Sprintf("BRANCH-FOLLOWED %s — PR %s is headed by %s, not the recorded %s", t.Name, g.pr, info.Head, g.branch))
	g.branch, g.target.Branch, g.res.Branch = info.Head, info.Head, info.Head
	if !g.o.CheckOnly && t.Name != "" { // o.Slot is a PR ref when gated by number
		UpdateSlot(g.root, t.Name, func(s *Slot) { s.SetBranch(info.Head) })
	}
	return finish(false, nil)
}

// stepRemote decides whether the gate reads origin/* refs. A PR merges what was
// PUSHED, so with a PR and an origin remote the gate reads origin/* refs.
// Without a PR the local merge takes the local branch, and local refs are the
// right ones. A recorded PR with no origin remote is refused: a local merge
// would leave the PR open and bypass its review, CI and branch protection while
// reporting MERGED.
func (runner gateRun) stepRemote(result GateResult, pr string) (bool, GateResult, bool, error) {
	g := runner.operation(result, GateTarget{PR: pr}, gateForgeResult{})
	finish := func(done bool, err error) (bool, GateResult, bool, error) {
		return g.remote, *g.res, done, err
	}

	if g.pr == "" {
		return finish(false, nil)
	}
	g.prNum = prNumText(g.pr)
	// Only exit 2 ("No such remote") means origin is absent. A git that cannot
	// run (its directory gone, a signal, a timeout) or fails otherwise says
	// nothing about the remote, so it is reported as what it is.
	res, err := g.e.Git(g.ctx, g.root, "remote", "get-url", g.rem())
	switch {
	case err != nil:
		return finish(g.broke(fmt.Sprintf("could not run git in %s to read the %s remote: %v", g.root, g.rem(), err)))
	case res.ExitCode == 2:
		return finish(g.broke(fmt.Sprintf("slot %s has PR %s but this repo has no '%s' remote; refusing a local merge that would bypass the PR", g.o.Slot, g.pr, g.rem())))
	case res.ExitCode != 0:
		return finish(g.broke(fmt.Sprintf("git remote get-url %s failed in %s (exit %d): %s", g.rem(), g.root, res.ExitCode, strings.TrimSpace(res.Stderr))))
	}
	if g.prNum == "" {
		return finish(g.broke(fmt.Sprintf("cannot read a PR number from '%s'", g.pr)))
	}
	g.remote = true
	return finish(false, nil)
}

// stepAlreadyMerged reads the PR before judging its head, including check-only
// train members. It may have merged before this run or while waiting for the
// land lock; the branch may already be deleted.
func (runner gateRun) stepAlreadyMerged(result GateResult, target GateTarget, forged gateForgeResult, remote bool) (*tracker.PRInfo, GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.remote = remote
	var viewed *tracker.PRInfo
	finish := func(done bool, err error) (*tracker.PRInfo, GateResult, bool, error) {
		return viewed, *g.res, done, err
	}

	if !g.remote {
		return finish(false, nil)
	}
	info, ok := g.prInfo()
	if !ok {
		return finish(false, nil)
	}
	viewed = &info
	if info.State == "MERGED" && info.Base == g.o.Base {
		g.res.Branch = info.Head
		g.res.AlreadyMerged = true
		g.verdict(GateAlreadyMerged, fmt.Sprintf("ALREADY-MERGED %s — PR %s already merged into %s; nothing to merge", g.o.Slot, g.pr, g.o.Base), "")
		return finish(true, nil)
	}
	return finish(false, nil)
}

// stepRefs resolves the refs the later steps judge: fetched origin refs for a
// remote gate, the local branch otherwise.
func (runner gateRun) stepRefs(result GateResult, branch string, remote bool) (gateRefsResult, GateResult, bool, error) {
	g := runner.operation(result, GateTarget{Branch: branch}, gateForgeResult{})
	g.remote = remote
	finish := func(done bool, err error) (gateRefsResult, GateResult, bool, error) {
		return gateRefsResult{head: g.headRef, base: g.baseRef, localHead: g.verified}, *g.res, done, err
	}

	root, o := g.root, g.o
	// A remote gate may judge a base that lives only on the remote (the integration
	// branch a stacked one is built on); the fetched ref is checked below.
	_, local := g.git(root, "rev-parse", "--verify", "--quiet", o.Base)
	if local != 0 && g.remote {
		_, local = g.git(root, "rev-parse", "--verify", "--quiet", g.rem()+"/"+o.Base)
	}
	if local != 0 {
		return finish(true, fail(exitcode.ExitResolution, fmt.Sprintf("base branch '%s' does not exist", o.Base)))
	}
	if !g.remote {
		g.headRef, g.baseRef = g.branch, o.Base
		var code int
		// The commit the gates below judge is the commit that lands.
		if g.verified, code = g.git(root, "rev-parse", "--verify", "--quiet", g.branch); code != 0 || g.verified == "" {
			return finish(true, fail(exitcode.ExitResolution, fmt.Sprintf("worker branch '%s' does not exist", g.branch)))
		}
		return finish(false, nil)
	}
	if !o.Observe {
		if _, code := g.git(root, "fetch", g.rem(), "-q"); code != 0 {
			return finish(g.broke("git fetch " + g.rem() + " failed"))
		}
	}
	g.headRef, g.baseRef = g.rem()+"/"+g.branch, g.rem()+"/"+o.Base
	if _, code := g.git(root, "rev-parse", "--verify", "--quiet", g.headRef+"^{commit}"); code != 0 {
		return finish(g.broke(fmt.Sprintf("%s does not exist — has the worker pushed %s?", g.headRef, g.branch)))
	}
	if _, code := g.git(root, "rev-parse", "--verify", "--quiet", g.baseRef+"^{commit}"); code != 0 {
		return finish(g.broke(g.baseRef + " does not exist"))
	}
	return finish(false, nil)
}

// issueSide is the issue source the gate reads beside forge, built from
// issues.source: none for a file source; the forge adapter itself when the
// source is that forge's own tracker (github or gitlab matching the forge's
// provider, forge, or an unmigrated legacy config); else the tracker the source
// names (Env.Issues).
func (e Env) issueSide(forge Forge, forgeProvider, dir string, s tracker.Settings) (Issues, error) {
	switch s.Source {
	case config.SourceFile, "":
		return nil, nil
	case config.SourceForge, config.SourceIssues, forgeProvider:
		if is, ok := forge.(Issues); ok {
			return is, nil
		}
	}
	return e.Issues(dir, s)
}

// prInfo reads the PR; ok is false when the forge could not be read.
func (g *gateRun) prInfo() (tracker.PRInfo, bool) {
	n, err := strconv.Atoi(g.prNum)
	if err != nil {
		return tracker.PRInfo{}, false
	}
	info, err := g.forge.PRView(g.ctx, n)
	return info, err == nil
}

// openPRForBranch finds the open PR headed by the gate's branch. Without an
// origin remote or a forge CLI there is no remote to hold one, and the local
// merge stands. Any other failure to ask is a check-broke message: guessing
// "none" would fail open into a local merge of a branch whose PR may be open.
func (g *gateRun) openPRForBranch() (url, brokeMsg string) {
	if _, code := g.git(g.root, "remote", "get-url", g.rem()); code != 0 {
		return "", ""
	}
	prs, err := g.forge.OpenPRs(g.ctx)
	if err != nil {
		if tracker.IsCLIMissing(err) {
			return "", ""
		}
		return "", fmt.Sprintf("slot %s records no PR and the open PRs of %s could not be listed to look for one headed by %s: %v", g.o.Slot, g.provider, g.branch, err)
	}
	for _, p := range prs {
		if p.Branch == g.branch {
			return p.URL, ""
		}
	}
	return "", ""
}

// prBody is the PR/MR description. An error means it could not be read.
func (g *gateRun) prBody() (string, error) {
	info, err := g.prView()
	return info.Body, err
}

func (g *gateRun) prView() (tracker.PRInfo, error) {
	n, ok := PRRefNumber(g.pr)
	if !ok {
		return tracker.PRInfo{}, fmt.Errorf("no PR number in %q", g.pr)
	}
	return g.forge.PRView(g.ctx, n)
}
