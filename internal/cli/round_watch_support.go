package cli

import (
	"context"
	"regexp"
	"strconv"
	"strings"

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
//   - main only, for an offered issue with an open PR from the support machine:
//     pr-opened ("#N") and pr-head (the PR's head sha).
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
		for _, s := range worker.LoadRegistryTolerant(root).Slots() {
			if id := worker.NormID(s.HeldID()); id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
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
	if len(ids) == 0 {
		return out
	}
	trust := worker.NewMarkerTrust(ctx, env.Forge)
	for _, id := range ids {
		if is, err := env.Forge.Get(ctx, tracker.IssueKey(id), true); err == nil {
			if c, ok := otherSide(trust, onSupport, is.Comments); ok {
				out[roundwatch.SupportKey(id, roundwatch.SupportComment)] = c.Author + "#" + c.ID
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
	for _, id := range ids {
		pr, ok := supportPRFor(env, prs, id)
		if !ok {
			continue
		}
		out[roundwatch.SupportKey(id, roundwatch.SupportPROpened)] = "#" + strconv.Itoa(pr.Number)
		if v, err := env.Forge.PRView(ctx, pr.Number); err == nil && v.HeadSHA != "" {
			out[roundwatch.SupportKey(id, roundwatch.SupportPRHead)] = v.HeadSHA
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

// supportPRFor finds the open PR the support machine built for issue id: its
// branch is `<round.support>-<agent>/<id>-<slug>`, or it closes the issue.
func supportPRFor(env round.Env, prs []tracker.PR, id string) (tracker.PR, bool) {
	n, err := strconv.Atoi(id)
	if err != nil {
		return tracker.PR{}, false
	}
	for _, pr := range prs {
		if !strings.HasPrefix(pr.Branch, env.Round.Support+"-") {
			continue
		}
		if m := branchIssue.FindStringSubmatch(pr.Branch); m != nil && m[1] == id {
			return pr, true
		}
		for _, c := range env.Forge.ClosedNumbers(pr.Body) {
			if c == n {
				return pr, true
			}
		}
	}
	return tracker.PR{}, false
}
