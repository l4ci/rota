package round

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// findUnregisteredBranches reports every local or remote branch that matches
// round.adoptPattern, that no slot holds and that is not merged into base. A
// name that yields an issue number is repairable (adopt); the rest need
// `rota worker adopt --issue`. On main with round.support set, the support
// machine's branches are findSupportBranches' alone, whatever the pattern.
func (e Env) findUnregisteredBranches(ctx context.Context, root string, rep *Report, reg worker.Registry) {
	held := map[string]bool{}
	for _, s := range reg.Slots() {
		held[s.Branch()] = true
	}
	support := e.supportPrefix()
	if support != "" {
		e.findSupportBranches(ctx, rep, held, support)
	}
	if e.Round.AdoptPattern == "" {
		return
	}
	if _, err := path.Match(e.Round.AdoptPattern, ""); err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("round.adoptPattern: %v", err))
		return
	}
	res, err := e.Git(ctx, root, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes/origin")
	if err != nil || res.ExitCode != 0 {
		return
	}
	seen := map[string]bool{}
	for _, ref := range strings.Fields(res.Stdout) {
		name := strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/origin/")
		if name == "HEAD" || seen[name] || held[name] || name == e.Base || worker.IsPark(name) {
			continue
		}
		if support != "" && strings.HasPrefix(name, support) {
			continue
		}
		if ok, _ := path.Match(e.Round.AdoptPattern, name); !ok {
			continue
		}
		seen[name] = true
		if r, _ := e.Git(ctx, root, "merge-base", "--is-ancestor", ref, e.Base); r.ExitCode == 0 {
			continue // nothing to land
		}
		f := Finding{Kind: UnregisteredBranch, Issue: worker.IssueFromBranch(name), branch: name}
		if f.Issue != "" {
			f.Detail = fmt.Sprintf("%s matches round.adoptPattern and no slot holds it (%s)", name, tracker.RefOf(f.Issue))
			f.Repair = "adopt as an external slot"
		} else {
			f.Detail = fmt.Sprintf("%s matches round.adoptPattern and no slot holds it: needs --issue (rota worker adopt %s --issue N)", name, name)
		}
		rep.add(f)
	}
}

// supportPrefix is "<round.support>-" on the main machine with the support
// split on (#1598), "" otherwise: the support machine never adopts its own
// branches.
func (e Env) supportPrefix() string {
	if e.Round.Support == "" || e.Round.Role == roundcfg.RoleSupport {
		return ""
	}
	return e.Round.Support + "-"
}

// findSupportBranches reports the support machine's finished work (#1598
// AC-6): an open PR headed by `<round.support>-*/*` that no slot holds, whose
// issue carries issues.labels.needsReview. A support branch still in flight
// (no PR yet, or its issue not handed to review) stays the support machine's
// and is never reported. It reads the forge's open PRs, not refs, so main sees
// the PR before it has fetched the branch; adopting it fetches the branch.
func (e Env) findSupportBranches(ctx context.Context, rep *Report, held map[string]bool, prefix string) {
	if rep.openPRs == nil || e.Forge == nil {
		return
	}
	var cands []*tracker.PR
	for branch, pr := range rep.openPRs {
		if ok, _ := path.Match(prefix+"*/*", branch); ok && !held[branch] && strings.HasPrefix(branch, prefix) {
			cands = append(cands, pr)
		}
	}
	if len(cands) == 0 {
		return
	}
	label := strutil.FirstNonEmpty(e.NeedsReview, DefaultNeedsReview)
	issues, err := e.Forge.List(ctx, tracker.ListFilter{State: "open", Labels: []string{label}})
	if err != nil {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("support PRs not checked: listing the %s issues failed: %v", label, err))
		return
	}
	review := map[tracker.IssueKey]bool{}
	for _, is := range issues {
		review[is.Key] = true
	}
	slices.SortFunc(cands, func(a, b *tracker.PR) int { return a.Number - b.Number })
	for _, pr := range cands {
		issue := worker.IssueFromBranch(pr.Branch)
		key, ok := tracker.ParseKey(issue)
		if !ok || !review[key] {
			continue
		}
		rep.add(Finding{Kind: UnregisteredBranch, Issue: issue, branch: pr.Branch, pr: pr.URL, support: true,
			Detail: fmt.Sprintf("%s is the support machine's PR #%d for %s, handed to review (%s), and no slot holds it", pr.Branch, pr.Number, tracker.RefOf(issue), label),
			Repair: "adopt as an external slot"})
	}
}

// adoptBranch registers the finding's branch as an external slot. A remote-only
// branch gets a local branch first; a support branch is fetched for it, since
// main may not have seen it yet. A refusal (a blocking overlap, a held issue)
// comes back as the error, which Reconcile reports as a warning.
func (e Env) adoptBranch(ctx context.Context, root string, f Finding) error {
	if e.Board == nil {
		return fmt.Errorf("no backlog to check %s against", f.branch)
	}
	if r, _ := e.Git(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+f.branch); r.ExitCode != 0 {
		if f.support {
			if r, err := e.Git(ctx, root, "fetch", "-q", "origin", "+refs/heads/"+f.branch+":refs/remotes/origin/"+f.branch); err != nil || r.ExitCode != 0 {
				return fmt.Errorf("could not fetch %s from origin", f.branch)
			}
		}
		if r, err := e.Git(ctx, root, "branch", "--track", f.branch, "origin/"+f.branch); err != nil || r.ExitCode != 0 {
			return fmt.Errorf("could not create local branch %s from origin", f.branch)
		}
	}
	_, err := e.Adopt(ctx, root, e.Board, AdoptOpts{Ref: f.branch, Issue: f.Issue, PR: f.pr, Shared: e.Round.SharedPaths})
	return err
}

// findSupportBounced reports, on the support machine, each queued PR main sent
// back (#1598 AC-8): the PR's last review-bounce comment names its current
// head, so no fix was pushed since. A slot still holding its PR hears of the
// bounce through the review poll; a queued PR has no worker, so the
// orchestrator resumes it on a free slot. It is reported, never repaired.
func (e Env) findSupportBounced(ctx context.Context, rep *Report, reg worker.Registry) {
	if e.Round.Support == "" || e.Round.Role != roundcfg.RoleSupport || e.Forge == nil {
		return
	}
	rf, ok := e.Forge.(worker.ReviewForge)
	if !ok {
		return
	}
	for _, q := range reg.PRs() {
		n, ok := worker.PRRefNumber(q.PR)
		if !ok {
			continue
		}
		notes, err := rf.MRNotes(ctx, n)
		if err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("bounces on %s not read: %v", q.PR, err))
			continue
		}
		head := ""
		for _, c := range notes {
			if kind, h, _, ok := marker.ParseBounce(c.Body); ok && kind == marker.KindReviewBounce {
				head = h
			}
		}
		if head == "" {
			continue
		}
		info, err := e.Forge.PRView(ctx, n)
		if err != nil {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("bounce on %s not checked: %v", q.PR, err))
			continue
		}
		if !strings.EqualFold(info.State, "OPEN") || info.HeadSHA == "" || !(strings.HasPrefix(info.HeadSHA, head) || strings.HasPrefix(head, info.HeadSHA)) {
			continue
		}
		rep.add(Finding{Kind: Bounced, Issue: q.Issue, pr: q.PR, branch: q.Branch,
			Detail: fmt.Sprintf("main sent %s back at %s and %s has not moved since: rota round transfer %s --to <free slot> resumes it on its branch", q.PR, strutil.ShortSHA(head), q.Branch, q.Issue)})
	}
}
