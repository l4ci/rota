package cli

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/roundwatch"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// supportSnapshot is the support split's part of the watch snapshot (#1827),
// empty unless round.support is set. It reads only the offered-or-held issues,
// once per forge poll, never the backlog:
//
//   - on main the offered set is the open issues carrying the support label
//     (one label query); on the support machine it is the issues its slots hold;
//   - comment: the newest comment on such an issue that the OTHER machine posted
//     and a writer-trusted author wrote (see otherSide);
//   - support only (#1911): the held issues include those of the queued
//     review-list PRs, and every open PR of this machine, held or queued, that
//     the forge reports as conflicting with its base adds a conflict entry
//     (the PR's head sha, so it wakes once per head);
//   - main only, for every open PR from the support machine (branch prefix
//     `<round.support>-`), offered or not (#1918): pr-opened ("#N") and pr-head
//     (the PR's head sha); and for a support branch on origin with no open PR,
//     branch-head (the branch's sha).
//
// A read that fails leaves its entries out, which the watch reads as no news.
func supportSnapshot(ctx context.Context, env round.Env, root string) map[string]string {
	out := map[string]string{}
	if env.Round.Support == "" || env.Forge == nil {
		return out
	}
	onSupport := env.Round.Role == roundcfg.RoleSupport
	var ids []string
	if onSupport {
		seen := map[string]bool{}
		add := func(raw string) {
			if id := worker.NormID(raw); id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		reg := worker.LoadRegistryTolerant(root)
		for _, s := range reg.Slots() {
			add(s.HeldID())
		}
		for _, q := range reg.PRs() {
			for _, id := range q.Issues() {
				add(id)
			}
		}
		for id, v := range supportConflicts(ctx, env, reg) {
			out[roundwatch.SupportKey(id, roundwatch.SupportConflict)] = v
		}
	} else {
		offered, err := env.Forge.List(ctx, tracker.ListFilter{State: "open", Labels: []string{env.SupportLabel}})
		if err != nil {
			return out
		}
		for _, is := range offered {
			ids = append(ids, worker.NormID(is.Key.String()))
		}
	}
	if len(ids) > 0 {
		trust := worker.NewMarkerTrust(ctx, env.Forge)
		for _, id := range ids {
			if is, err := env.Forge.Get(ctx, tracker.IssueKey(id), true); err == nil {
				if c, ok := otherSide(trust, onSupport, is.Comments); ok {
					out[roundwatch.SupportKey(id, roundwatch.SupportComment)] = c.Author + "#" + c.ID
				}
			}
		}
	}
	if onSupport {
		return out
	}
	prs, err := env.Forge.OpenPRs(ctx)
	if err != nil {
		return out
	}
	hasPR := map[string]bool{}
	for _, pr := range prs {
		if !strings.HasPrefix(pr.Branch, env.Round.Support+"-") {
			continue
		}
		hasPR[pr.Branch] = true
		id := supportPRID(env, pr)
		out[roundwatch.SupportKey(id, roundwatch.SupportPROpened)] = "#" + strconv.Itoa(pr.Number)
		if v, err := env.Forge.PRView(ctx, pr.Number); err == nil && v.HeadSHA != "" {
			out[roundwatch.SupportKey(id, roundwatch.SupportPRHead)] = v.HeadSHA
		}
	}
	for branch, sha := range supportBranchHeads(ctx, env, root) {
		if !hasPR[branch] {
			out[roundwatch.SupportKey(roundwatch.SupportBranchID(branch), roundwatch.SupportBranchHead)] = sha
		}
	}
	return out
}

// prMergeability is the forge's mergeability read, which round.Forge leaves
// out: the real forges have it, fakes need not.
type prMergeability interface {
	PRMergeable(ctx context.Context, pr int) (tracker.Mergeability, error)
}

// supportConflicts maps the issue of each open PR this machine holds or has
// queued to the PR's head sha when the forge says it conflicts with its base.
// A forge without the read, a closed PR, an unknown answer and a failed read
// all leave the PR out: no news.
func supportConflicts(ctx context.Context, env round.Env, reg worker.Registry) map[string]string {
	out := map[string]string{}
	m, ok := env.Forge.(prMergeability)
	if !ok {
		return out
	}
	check := func(id, pr string) {
		n, ok := worker.PRRefNumber(pr)
		id = worker.NormID(id)
		if !ok || id == "" {
			return
		}
		v, err := env.Forge.PRView(ctx, n)
		if err != nil || !strings.EqualFold(v.State, "open") || v.HeadSHA == "" {
			return
		}
		if mg, err := m.PRMergeable(ctx, n); err == nil && mg.State == tracker.MergeConflict {
			out[id] = v.HeadSHA
		}
	}
	for _, s := range reg.Slots() {
		if s.PR() != "" {
			check(s.HeldID(), s.PR())
		}
	}
	for _, q := range reg.PRs() {
		check(q.Issue, q.PR)
	}
	return out
}

// supportBranchHeads maps each `<round.support>-*` branch on origin to its head
// sha: the pushes of a branch that has no PR yet, which the forge cannot show.
// A failed read is no news.
func supportBranchHeads(ctx context.Context, env round.Env, root string) map[string]string {
	out := map[string]string{}
	if env.Git == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	res, err := env.Git(ctx, root, "ls-remote", "--heads", "origin", "refs/heads/"+env.Round.Support+"-*")
	if err != nil || res.ExitCode != 0 {
		return out
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if branch, found := strings.CutPrefix(ref, "refs/heads/"); ok && found && sha != "" {
			out[branch] = sha
		}
	}
	return out
}

// otherSide is the newest comment the other machine posted. The machines
// usually share one forge login, so the author cannot tell them apart; the
// comment says it: a `rota:from main|support` stamp, or a kind only one
// machine posts (marker.SideOf: the support-offer comment is main's, the
// support-return and relay comments the support machine's). A comment without
// either, a human's or an old one, is nobody's and never wakes the watch, and
// so is one whose author lacks write access (the same trust the gate applies
// to markers).
func otherSide(trust *worker.MarkerTrust, onSupport bool, comments []tracker.Comment) (tracker.Comment, bool) {
	other := roundcfg.RoleSupport
	if onSupport {
		other = roundcfg.RoleMain
	}
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if c.ID == "" || marker.SideOf(c.Body) != other {
			continue
		}
		if ok, err := trust.Writer(c.Author); err == nil && ok {
			return c, true
		}
	}
	return tracker.Comment{}, false
}

var branchIssue = regexp.MustCompile(`/(\d+)-`)

// supportPRID names a support PR's entries: the issue its branch
// `<round.support>-<agent>/<id>-<slug>` carries, else the first issue it
// closes, else the encoded branch.
func supportPRID(env round.Env, pr tracker.PR) string {
	if m := branchIssue.FindStringSubmatch(pr.Branch); m != nil {
		return m[1]
	}
	if c := env.Forge.ClosedNumbers(pr.Body); len(c) > 0 {
		return strconv.Itoa(c[0])
	}
	return roundwatch.SupportBranchID(pr.Branch)
}
