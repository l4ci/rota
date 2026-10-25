package round

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

const StaleClaimKind = blockedby.StaleClaim

type StaleClaim struct {
	Issue, Holder string
	Last          time.Time
	Threshold     time.Duration
}

func staleThreshold(it backlog.Item, s roundcfg.Settings) time.Duration {
	minutes := s.ClaimStaleMinutes
	if it.Tag == "Major" {
		minutes = s.ClaimStaleMinutesMajor
	}
	return time.Duration(minutes) * time.Minute
}

// StaleClaims conservatively reads activity for claims outside this machine's
// registry. Unknown timestamps or failed activity reads never permit reclaim.
// A nil key set scans labelled open items; explicit keys bound an assign recheck.
func (e Env) StaleClaims(ctx context.Context, root string, be Board, keys []tracker.IssueKey) ([]StaleClaim, []string) {
	if !e.forgeOn(be) || e.Round.ClaimStaleMinutes <= 0 && e.Round.ClaimStaleMinutesMajor <= 0 {
		return nil, nil
	}
	all, err := be.List(false)
	if err != nil {
		return nil, []string{fmt.Sprintf("stale claims: %v", err)}
	}
	items := e.claimedItems(all)
	if keys != nil {
		items = slices.DeleteFunc(items, func(it backlog.Item) bool { return !slices.Contains(keys, it.IssueKey()) })
	}
	if len(items) == 0 {
		return nil, nil
	}
	prs, err := e.Forge.OpenPRs(ctx)
	if err != nil {
		return nil, []string{fmt.Sprintf("stale claims: %v", err)}
	}
	return e.staleClaims(ctx, root, be, items, prs)
}

func (e Env) hasClaimSignal(it backlog.Item) bool {
	label := e.Label
	if label == "" {
		label = "in-progress"
	}
	return slices.ContainsFunc(it.Labels, func(l string) bool { return l == label || strings.HasPrefix(l, backlog.ClaimLabelPrefix) })
}
func (e Env) claimedItems(items []backlog.Item) []backlog.Item {
	var out []backlog.Item
	for _, it := range items {
		if !it.Closed && e.hasClaimSignal(it) {
			out = append(out, it)
		}
	}
	return out
}

// staleClaims shares already-read items and PRs with candidates and status.
func (e Env) staleClaims(ctx context.Context, root string, be Board, items []backlog.Item, prs []tracker.PR) ([]StaleClaim, []string) {
	if !e.forgeOn(be) || e.Round.ClaimStaleMinutes <= 0 && e.Round.ClaimStaleMinutesMajor <= 0 {
		return nil, nil
	}
	activity := map[tracker.IssueKey]time.Time{}
	branches := map[tracker.IssueKey][]string{}
	for _, pr := range prs {
		for key := range e.issuesOfPRs([]tracker.PR{pr}, "") {
			if pr.UpdatedAt.After(activity[key]) {
				activity[key] = pr.UpdatedAt
			}
			branches[key] = append(branches[key], pr.Branch)
		}
	}
	held := heldIDs(root)
	now := e.now()
	var out []StaleClaim
	var warnings []string
	for _, it := range items {
		key := it.IssueKey()
		if held[worker.NormID(key.String())] {
			continue
		}
		threshold := staleThreshold(it, e.Round)
		if threshold <= 0 {
			continue
		}
		st, err := be.Status(key.String())
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("stale claim %s: %v", key.Ref(), err))
			continue
		}
		if st == nil {
			continue
		}
		for _, holder := range openClaimsOf(st) {
			last := st.ClaimedAt[holder]
			if last.IsZero() {
				continue
			}
			if activity[key].After(last) {
				last = activity[key]
			}
			if now.Sub(last) <= threshold {
				continue
			}
			branchLast, err := e.claimBranchActivity(ctx, root, key, branches[key], now)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("stale claim %s (%s): %v", key.Ref(), holder, err))
				continue
			}
			if branchLast.After(last) {
				last = branchLast
			}
			if now.Sub(last) > threshold {
				out = append(out, StaleClaim{Issue: key.String(), Holder: holder, Last: last, Threshold: threshold})
			}
		}
	}
	return out, warnings
}

func (e Env) claimBranchActivity(ctx context.Context, root string, key tracker.IssueKey, prBranches []string, now time.Time) (time.Time, error) {
	run := e.Git
	if run == nil {
		run = git.Exec
	}
	args := []string{"ls-remote", "--heads", "origin", "refs/heads/*/" + key.String() + "-*"}
	for _, branch := range prBranches {
		args = append(args, "refs/heads/"+branch)
	}
	res, err := run(ctx, root, args...)
	if err != nil {
		return time.Time{}, err
	}
	if res.ExitCode != 0 {
		return time.Time{}, fmt.Errorf("cannot read origin branches: %s", strings.TrimSpace(res.Stderr))
	}
	var last time.Time
	for _, line := range strings.Split(res.Stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		branch := strings.TrimPrefix(fields[1], "refs/heads/")
		if branchKey, ok := worker.BranchIssue(branch); (!ok || branchKey != key) && !slices.Contains(prBranches, branch) {
			continue
		}
		res, err = run(ctx, root, "fetch", "--no-tags", "origin", fields[1])
		if err != nil {
			return time.Time{}, err
		}
		if res.ExitCode != 0 {
			return time.Time{}, fmt.Errorf("cannot fetch %s", branch)
		}
		// Read the exact advertised commit, never a local branch with unpushed work.
		res, err = run(ctx, root, "log", "-1", "--format=%cI", fields[0])
		if err != nil {
			return time.Time{}, err
		}
		if res.ExitCode != 0 {
			return time.Time{}, fmt.Errorf("cannot date %s", branch)
		}
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(res.Stdout))
		if err != nil {
			return time.Time{}, err
		}
		if at.After(now) {
			at = now
		}
		if at.After(last) {
			last = at
		}
	}
	return last, nil
}

// checkStaleClaim is repeated at the mutation boundary, after any time spent
// obtaining approval or preparing the worker. It changes no remote state.
func (e Env) checkStaleClaim(ctx context.Context, root string, be Board, key tracker.IssueKey, holder string) error {
	st, err := be.Status(key.String())
	if err != nil {
		return blocked(BlockNotStale, "cannot verify claim %s on %s: %v", holder, key.Ref(), err)
	}
	if st == nil || st.Claim != holder {
		return blocked(BlockNotStale, "claim %s is not the current holder of %s", holder, key.Ref())
	}
	claims, warnings := e.StaleClaims(ctx, root, be, []tracker.IssueKey{key})
	for _, c := range claims {
		if c.Holder == holder {
			return nil
		}
	}
	return blocked(BlockNotStale, "claim %s on %s is not stale (or activity cannot be verified): %s", holder, key.Ref(), strings.Join(warnings, "; "))
}
