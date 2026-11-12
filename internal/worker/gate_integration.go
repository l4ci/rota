package worker

import (
	"context"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
)

// The gate's side of an integration branch (#3096): the PR that lands the
// branch, the review of its fix commits, and the slots that fix it.

// integrationLanding is the unsettled integration branch whose PR o targets
// (open, or landed with its cleanup not yet complete); nil for any other gate. It is matched by the recorded PR, never by a branch
// name, so a PR whose branch only looks like one gets no integration rule. An
// unreadable registry is an error: the gate cannot tell whether the integration
// rules apply, so it must not land as if they did not.
func (e Env) integrationLanding(root string, o GateOpts) (*Integration, error) {
	if o.CheckOnly || o.LandVerified {
		return nil, nil
	}
	reg, err := LoadRegistry(root)
	if err != nil {
		return nil, err
	}
	ref := o.Slot
	if gt, err := reg.GateTargetAny(o.Slot); err == nil && gt.PR != "" {
		ref = gt.PR
	}
	if rec, ok := reg.IntegrationByPR(ref); ok && !rec.Settled {
		return &rec, nil
	}
	return nil, nil
}

// landIntegration gates an integration PR under its integration lock and the
// land lock. It reads the record again there, so a train that finished while
// this gate waited is seen, then runs the preflight and the gate. A PR the
// forge already merged (a retry after a local step failed) still gets the
// member bookkeeping, which nothing else would run.
func (e Env) landIntegration(ctx context.Context, root string, o GateOpts, branch string, res *GateResult) error {
	broke := func(msg string) error {
		*res = GateResult{Slot: o.Slot, Base: o.Base, Branch: branch, Verdict: GateCheckBroke, Err: checkBrokeMessage(o.Slot, msg)}
		return nil
	}
	reg, err := LoadRegistry(root)
	if err != nil {
		return broke("cannot read the integration record of " + branch + ": " + err.Error())
	}
	rec, ok := reg.IntegrationByBranch(branch)
	if !ok {
		return broke("the integration record of " + branch + " is gone")
	}
	o.HoldsLandLock = true
	// A PR the forge merged (the record says so, or the forge does after a
	// retry whose local confirmation failed) is not a new landing: the member
	// branches may be deleted or moved since, which only a new landing cares about.
	merged := rec.Landed || e.integrationMerged(ctx, root, o, rec)
	var notes []string
	if !merged {
		refused, n := e.integrationPreflight(ctx, root, &o, rec)
		if refused != nil {
			*res = *refused
			return nil
		}
		notes = n
	}
	r, err := e.gate(ctx, root, o)
	*res = r
	if err != nil {
		return err
	}
	res.Notes = append(notes, res.Notes...)
	if res.Verdict == GatePass || res.Verdict == GateAlreadyMerged {
		return integrationLanded(root, rec, res)
	}
	return nil
}

// integrationMerged reports whether the forge already merged the PR of rec into
// the gate's base. Any failure to ask answers false: the landing then runs its
// preflight as for a new one.
func (e Env) integrationMerged(ctx context.Context, root string, o GateOpts, rec Integration) bool {
	n, ok := PRRefNumber(rec.PR)
	if !ok {
		return false
	}
	in, err := e.GateInput(root)
	if err != nil {
		return false
	}
	f, _, refusal := e.openForge(ctx, root, rec.PR, in.cfg, tracker.ForgeActor)
	if refusal != nil {
		return false
	}
	info, err := f.PRView(ctx, n)
	return err == nil && info.State == "MERGED" && info.Base == o.Base
}

// integrationPreflight runs before the gate of an integration PR, under its
// integration lock and the land lock: a pending member, one whose head moved
// since its merge or whose branch is gone refuses the landing, and a tip the
// train verified on the base as it is now lands without a second verify
// (LandVerified, bound to that tip and base). Any other tip runs the gate's
// own verify, which merges the current base into it first.
func (e Env) integrationPreflight(ctx context.Context, root string, o *GateOpts, rec Integration) (*GateResult, []string) {
	rerun := fmt.Sprintf("re-run `rota worker train %s %s`", strings.Join(memberTargetsOf(rec), " "), rec.TrainFlags())
	refuse := func(verdict, msg, hint string) *GateResult {
		return &GateResult{Slot: o.Slot, Base: o.Base, Branch: rec.Branch, PR: rec.PR, Verdict: verdict,
			Err: fmt.Sprintf("INTEGRATION %s refused — %s; nothing landed", rec.Branch, msg), Hint: hint}
	}
	var stackNotes []string
	if rec.Onto != "" {
		moved, notes, refused := e.settleStack(ctx, root, rec)
		if refused != nil {
			return refuse(refused.verdict, refused.msg, refused.hint), nil
		}
		rec, stackNotes = moved, notes
		o.Base = rec.Base
	}
	// The record alone decides a pending member: no remote is needed for it.
	for _, m := range rec.Members {
		if m.Pending {
			return refuse(GateStale, fmt.Sprintf("member %s does not merge yet (conflict in %s)", m.Target, strings.Join(m.Conflicts, ", ")),
				"resolve it through the interaction item on "+rec.Branch+", then "+rerun), nil
		}
	}
	in, err := e.GateInput(root)
	if err != nil {
		return refuse(GateCheckBroke, "cannot read the gate input: "+err.Error(), "fix the project config, then gate the PR again"), nil
	}
	rem := in.cfg.RemoteName()
	if out, code := e.runGit(ctx, root, "fetch", "-q", "--prune", rem); code != 0 { // prune: a deleted member branch must not read as unmoved
		return refuse(GateCheckBroke, fmt.Sprintf("git fetch %s failed: %s", rem, strings.TrimSpace(out)), "gate the PR again once the remote answers"), nil
	}
	for _, m := range rec.Members {
		if m.Branch == "" {
			continue
		}
		cur, code := e.git(root, "rev-parse", "--verify", "--quiet", rem+"/"+m.Branch)
		if code != 0 {
			return refuse(GateStale, fmt.Sprintf("member %s's branch %s is gone from %s, so it cannot be told whether it moved", m.Target, m.Branch, rem),
				"push the branch back at "+strutil.ShortSHA(m.Head)+", or drop the member and "+rerun), nil
		}
		if cur != m.Head {
			return refuse(GateStale, fmt.Sprintf("member %s moved from %s to %s since it was merged", m.Target, strutil.ShortSHA(m.Head), strutil.ShortSHA(cur)),
				rerun+": it merges the new head, which needs a fresh review first"), nil
		}
	}
	tip, _ := e.git(root, "rev-parse", "--verify", "--quiet", rem+"/"+rec.Branch)
	base, _ := e.git(root, "rev-parse", "--verify", "--quiet", rem+"/"+rec.Base)
	var note string
	switch {
	case rec.VerifiedTip == "":
		note = fmt.Sprintf("INTEGRATION-REVERIFY %s — no green train verified it; verifying the tip", rec.Branch)
	case tip != rec.VerifiedTip:
		note = fmt.Sprintf("INTEGRATION-REVERIFY %s — the tip moved from %s to %s since the train verified it", rec.Branch, strutil.ShortSHA(rec.VerifiedTip), strutil.ShortSHA(tip))
	case base != rec.VerifiedBase:
		note = fmt.Sprintf("INTEGRATION-REVERIFY %s — %s moved from %s to %s since the verify; merging it and verifying again", rec.Branch, rec.Base, strutil.ShortSHA(rec.VerifiedBase), strutil.ShortSHA(base))
	default:
		o.LandVerified, o.LandVerifiedTip, o.LandVerifiedBase = true, tip, base
		note = fmt.Sprintf("INTEGRATION-VERIFIED %s — the train verified tip %s on %s %s; no second verify", rec.Branch, strutil.ShortSHA(tip), rec.Base, strutil.ShortSHA(base))
	}
	return nil, append(stackNotes, note)
}

// integrationLanded is the bookkeeping of a landed integration PR: the member
// PRs leave the review queue, the record is marked landed and the result names
// the member issues, which the post-gate bookkeeping ends one by one.
func integrationLanded(root string, rec Integration, res *GateResult) error {
	for _, m := range rec.Members {
		if m.PR != "" {
			if err := RemoveQueuedPR(root, m.PR); err != nil {
				return err
			}
		}
		if m.Issue != "" {
			res.IntegrationMembers = append(res.IntegrationMembers, m.Issue)
		}
	}
	return UpdateIntegration(root, rec.Branch, func(it *Integration) { it.Landed = true })
}

func memberTargetsOf(rec Integration) []string {
	var out []string
	for _, m := range rec.Members {
		out = append(out, m.Target)
	}
	return out
}

// FixCommits are the first-parent commits after the branch's start up to tip
// that rota did not merge itself: conflict resolutions and interaction fixes.
// Member commits arrive as second parents of rota's merges and never count.
func (e Env) FixCommits(root string, rec Integration, from, tip string) ([]string, error) {
	if from == "" {
		from = rec.Start
	}
	out, code := e.git(root, "rev-list", "--first-parent", from+".."+tip)
	if code != 0 {
		return nil, fmt.Errorf("git rev-list --first-parent %s..%s exited %d", strutil.ShortSHA(from), strutil.ShortSHA(tip), code)
	}
	var fixes []string
	for _, c := range strings.Fields(out) {
		if !rec.IsRotaMerge(c) {
			fixes = append(fixes, c)
		}
	}
	return fixes, nil
}

// ReviewSince is where a review of the branch's fix commits starts: the
// parent of the oldest fix, so the reviewer sees every fix and nothing of the
// members merged before it. With no fix it is the assembled tip.
func (e Env) ReviewSince(root string, rec Integration, tip string) (string, error) {
	fixes, err := e.FixCommits(root, rec, "", tip)
	if err != nil || len(fixes) == 0 {
		return rec.Assembled, err
	}
	parent, code := e.git(root, "rev-parse", fixes[len(fixes)-1]+"^1")
	if code != 0 {
		return "", fmt.Errorf("git rev-parse %s^1 exited %d", strutil.ShortSHA(fixes[len(fixes)-1]), code)
	}
	return parent, nil
}

// integrationReviewed answers the review rule of an integration branch: a
// tip whose first-parent commits are all rota's merges needs no review;
// otherwise a review that covers depth must be recorded at a commit S after
// which only rota's merges follow. ok reports the rule held; note says why.
func (e Env) integrationReviewed(root string, rec Integration, list []verdict.Record, head string, covered func(sha string) bool) (ok bool, note string, err error) {
	fixes, err := e.FixCommits(root, rec, "", head)
	if err != nil {
		return false, "", err
	}
	if len(fixes) == 0 {
		return true, fmt.Sprintf("INTEGRATION-REVIEW %s — only rota's merges since %s; no fix to review", rec.Branch, strutil.ShortSHA(rec.Start)), nil
	}
	seen := map[string]bool{}
	for _, r := range list {
		if r.Sha == "" || seen[r.Sha] || !covered(r.Sha) {
			continue
		}
		seen[r.Sha] = true
		s, code := e.git(root, "rev-parse", "--verify", "--quiet", r.Sha+"^{commit}")
		if code != 0 {
			continue
		}
		if _, code := e.git(root, "merge-base", "--is-ancestor", s, head); code != 0 {
			continue
		}
		after, err := e.FixCommits(root, rec, s, head)
		if err != nil {
			return false, "", err
		}
		if len(after) == 0 {
			return true, fmt.Sprintf("INTEGRATION-REVIEW %s — %d fix commit(s) reviewed at %s", rec.Branch, len(fixes), strutil.ShortSHA(s)), nil
		}
	}
	return false, fmt.Sprintf("%d fix commit(s) after rota's merges (%s) have no passing review covering them", len(fixes), strutil.ShortSHA(fixes[0])), nil
}

// stepIntegrationBase refuses to land an interaction item's fix anywhere but
// its integration branch: on the base it would skip the branch's review, and
// on a member branch it would change a reviewed member.
func (runner gate) stepIntegrationBase(result GateResult, target GateTarget) (struct{}, GateResult, bool, error) {
	if target.Issue == "" {
		return struct{}{}, result, false, nil
	}
	reg, err := LoadRegistry(runner.root)
	if err != nil {
		result.Verdict = GateCheckBroke
		result.Err = checkBrokeMessage(runner.o.Slot, "cannot read the integration records: "+err.Error())
		return struct{}{}, result, true, nil
	}
	rec, ok := reg.IntegrationForItem(target.Issue)
	if !ok || runner.o.Base == rec.Branch {
		return struct{}{}, result, false, nil
	}
	result.Verdict = GatePRMismatch
	result.Err = fmt.Sprintf("GATE %s refused — %s is the interaction item of %s: its fix lands on that branch, not on %s; nothing landed", runner.o.Slot, target.Issue, rec.Branch, runner.o.Base)
	result.Hint = fmt.Sprintf("gate it with --base %s, or retarget its PR there", rec.Branch)
	return struct{}{}, result, true, nil
}
