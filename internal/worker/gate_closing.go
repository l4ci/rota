package worker

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/tracker"
)

// The closing, claim-fence and claim-release steps of the gate. Each judges
// through a small input struct and returns a stepOutcome, so a closing rule
// changes here and is tested without a gate fixture. The gate methods at the
// bottom only feed them from the gate and apply the outcome.

const GateUnmetAcceptance = "unmet acceptance"

// stepOutcome is what a closing-family check decided. The zero value passes.
// Verdict (with Msg and Hint) refuses the PR; Broke reports a check that could
// not run; Notes are non-fatal and kept whichever way the step ends.
type stepOutcome struct {
	Verdict string
	Msg     string
	Hint    string
	Broke   string
	Notes   []string
}

func (o stepOutcome) stops() bool { return o.Verdict != "" || o.Broke != "" }

// closesInput is what the closing check reads: the PR, the issues it must
// close and, when the forge can say so, what it closes by its own reckoning.
type closesInput struct {
	Slot       string
	PR         string
	Keys       []tracker.IssueKey // the lead issue, then its bundle members
	Source     tracker.ClosingSource
	FinalSlice bool
	Issues     Issues
	Forge      closingReporter // nil for a forge that cannot report closing refs
}

// checkCloses refuses a PR whose body does not close the slot's issue (a
// closing keyword: `Closes #N`, `Fixes #N`, ...; a key-based tracker: the key in
// the branch or PR title), unless that issue is labelled PartialSliceLabel.
// Without it the merge leaves the issue open with its claim and in-progress
// label, which reconcile reports as drift. A slot holding no issue has nothing
// to check.
func checkCloses(ctx context.Context, in closesInput) stepOutcome {
	if len(in.Keys) == 0 {
		return stepOutcome{}
	}
	out := checkClosingItems(ctx, in)
	if out.stops() {
		return out
	}
	// A bundled slot's PR closes the lead and every member: collect each one the
	// body leaves open, so one refusal names them all.
	var open []tracker.IssueKey
	for i, key := range in.Keys {
		if in.Issues.ClosesIssue(key, in.Source) {
			continue
		}
		is, err := in.Issues.Get(ctx, key, false)
		if err != nil {
			return stepOutcome{Broke: fmt.Sprintf("could not read %s to check for the %s label: %v", key.Ref(), PartialSliceLabel, err), Notes: out.Notes}
		}
		// a member closed since the assign has left the bundle (the lead, keys[0], stays)
		if i > 0 && is.State == "closed" {
			continue
		}
		if !slices.Contains(is.Labels, PartialSliceLabel) {
			open = append(open, key)
		}
	}
	if len(open) == 0 {
		return out
	}
	refs := refList(open)
	it, them := "it", "them"
	if len(open) == 1 {
		them = it
	}
	if _, numbered := open[0].Number(); numbered {
		lines := "a line `Closes " + open[0].Ref() + "`"
		if len(open) > 1 {
			var each []string
			for _, k := range open {
				each = append(each, "`Closes "+k.Ref()+"`")
			}
			lines = "the lines " + strings.Join(each, ", ")
		}
		return stepOutcome{Verdict: GateNotClosing, Notes: out.Notes,
			Msg:  fmt.Sprintf("GATE %s refused — the body of %s does not close %s, so the merge would leave %s open and claimed; nothing landed", in.Slot, in.PR, refs, them),
			Hint: fmt.Sprintf("add %s to the PR body, or label %s %s if this PR lands only part of %s", lines, refs, PartialSliceLabel, them)}
	}
	return stepOutcome{Verdict: GateNotClosing, Notes: out.Notes,
		Msg:  fmt.Sprintf("GATE %s refused — the branch and title of %s do not name %s, so the merge would leave %s open and claimed; nothing landed", in.Slot, in.PR, refs, them),
		Hint: fmt.Sprintf("put %s in the PR title or branch, or label %s %s if this PR lands only part of %s", refs, refs, PartialSliceLabel, them)}
}

// checkClosingItems refuses closing an item with unmet acceptance or a
// partial-slice parent with slices to come. The forge's closing list includes
// issues beyond the slot's own. FinalSlice exempts only the partial-slice rule;
// when the forge cannot report closing refs, acceptance still checks the
// slot's own items under the tracker's closing convention.
func checkClosingItems(ctx context.Context, in closesInput) stepOutcome {
	var nums []int
	known := false
	var closingErr error
	if in.Forge != nil {
		n, ok := PRRefNumber(in.PR)
		if !ok {
			return stepOutcome{Broke: fmt.Sprintf("cannot read a PR number from '%s' to ask what it closes", in.PR)}
		}
		var err error
		if nums, known, err = in.Forge.ClosingRefs(ctx, n); err != nil {
			// A forge that cannot answer the query is the same as one without the
			// capability: only a positive answer refuses, or every gate dies with it.
			nums, known = nil, false
			closingErr = err
		}
	}
	// Judge the slot's keys plus the forge's refs. A milestone merge closes the
	// slot's items explicitly even when the forge reports no auto-close refs.
	// The partial-slice rule still follows only the forge's list when known.
	cands := slices.Clone(in.Keys)
	if known {
		for _, n := range nums {
			key := tracker.NumKey(n)
			if !slices.Contains(cands, key) {
				cands = append(cands, key)
			}
		}
	}
	var leaks []tracker.IssueKey
	partial := false
	for _, key := range cands {
		number, numbered := key.Number()
		forgeCloses := known && numbered && slices.Contains(nums, number)
		is, err := in.Issues.Get(ctx, key, false)
		if err != nil {
			if !known {
				continue // preserve the existing optional-read fallback without forge refs
			}
			return stepOutcome{Broke: fmt.Sprintf("could not read %s to check for the %s label: %v", key.Ref(), PartialSliceLabel, err)}
		}
		if is.State != "closed" && (forgeCloses || in.Issues.ClosesIssue(key, in.Source)) && backlog.HasAcceptanceIDs(is.Body) {
			is, err = in.Issues.Get(ctx, key, true)
			if err != nil {
				return stepOutcome{Broke: fmt.Sprintf("could not read acceptance notes on %s: %v", key.Ref(), err)}
			}
			unmet, err := backlog.UnmetAcceptance(is.Body, is.Comments, proof.Lookup)
			if err != nil {
				return stepOutcome{Broke: fmt.Sprintf("could not read acceptance on %s: %v", key.Ref(), err)}
			}
			if len(unmet) > 0 {
				refusal := backlog.AcceptanceRefusal(key.String(), unmet)
				return stepOutcome{Verdict: GateUnmetAcceptance, Msg: fmt.Sprintf("GATE %s refused — %s; nothing landed", in.Slot, refusal.Msg), Hint: refusal.Hint}
			}
		}
		if numbered && (!known || forgeCloses) && !in.FinalSlice && slices.Contains(is.Labels, PartialSliceLabel) {
			partial = true
			leaks = append(leaks, key)
		}
	}
	if !known {
		// Nothing to judge: say so once, and only when a slice PR made it matter.
		if !partial {
			return stepOutcome{}
		}
		why := "the forge cannot report"
		if closingErr != nil {
			why = fmt.Sprintf("the forge could not report (%v)", closingErr)
		}
		return stepOutcome{Notes: []string{fmt.Sprintf("CLOSING-REFS-UNKNOWN %s — %s what %s closes; the %s check is skipped", in.Slot, why, in.PR, PartialSliceLabel)}}
	}
	if len(leaks) == 0 {
		return stepOutcome{}
	}
	refs := refList(leaks)
	return stepOutcome{Verdict: GateNotClosing,
		Msg:  fmt.Sprintf("GATE %s refused — the forge says %s closes %s, a %s issue with slices still to come; nothing landed", in.Slot, in.PR, refs, PartialSliceLabel),
		Hint: fmt.Sprintf("drop the closing keyword for %s from the PR body (use `Refs`), or pass --final-slice if this is the last slice", refs)}
}

// claimInput is what the claim fence reads: the claim the slot holds on each
// issue it works.
type claimInput struct {
	Slot    string
	ClaimID string
	Keys    []tracker.IssueKey
	Issues  Issues
}

// checkClaim fences a PR after another machine reclaimed its issue. A recorded
// claim must still be open; a target with no recorded claim has no fence.
// Unknown claims pass with a note, like other optional forge reads.
func checkClaim(ctx context.Context, in claimInput) stepOutcome {
	var out stepOutcome
	if in.ClaimID == "" {
		return out
	}
	for _, key := range in.Keys {
		held, err := backlog.HeldClaims(ctx, in.Issues, key)
		if err != nil {
			out.Notes = append(out.Notes, fmt.Sprintf("CLAIMS-UNKNOWN %s — could not read claims on %s: %v; the claim check is skipped", in.Slot, key.Ref(), err))
			continue
		}
		if len(held) == 0 || slices.Contains(held, in.ClaimID) {
			continue
		}
		out.Verdict = GateClaimLost
		out.Msg = fmt.Sprintf("GATE %s refused — claim %s no longer holds %s; %s does; nothing landed", in.Slot, in.ClaimID, key.Ref(), strings.Join(held, ", "))
		out.Hint = "the issue was reclaimed: stop the old holder and use the current holder's PR"
		return out
	}
	return out
}

// releaseInput is what the claim release reads: the labels to drop and the
// issues a merged PR closed.
type releaseInput struct {
	Slot   string
	Label  string // issues.labels.inProgress
	Keys   []tracker.IssueKey
	Issues Issues
}

// releaseClaimLabels drops in-progress and claimed:<agent>@<machine> labels
// from every issue the merged PR closed. The forge closes them but leaves the
// label, so the label would stop meaning "being worked on". An issue still open
// (the PR landed on a branch other than the default, or the forge has not
// closed it yet) keeps its label. Best effort: the PR is already merged, so a
// failure is a note, and `rota round reconcile --apply` clears whatever this
// missed.
func releaseClaimLabels(ctx context.Context, in releaseInput) (notes []string) {
	if in.Label == "" {
		return nil
	}
	for _, key := range in.Keys {
		if n := releaseClaimLabel(ctx, in, key); n != "" {
			notes = append(notes, n)
		}
	}
	return notes
}

// releaseClaimLabel asks backlog to release a closed issue's workflow labels.
// A failed write leaves a diagnostic; the merge has already landed.
func releaseClaimLabel(ctx context.Context, in releaseInput, key tracker.IssueKey) string {
	if in.Label == "" {
		return ""
	}
	drop, err := backlog.CleanupClosedIssueLabels(ctx, in.Issues, key, in.Label)
	if err == nil {
		return ""
	}
	if len(drop) == 0 {
		return fmt.Sprintf("LABEL-KEPT %s — cannot read %s to release %s: %v", in.Slot, key.Ref(), in.Label, err)
	}
	return fmt.Sprintf("LABEL-KEPT %s — cannot remove %s from %s: %v", in.Slot, strings.Join(drop, ", "), key.Ref(), err)
}

// stepCloses runs checkCloses on the slot's PR. It runs under CheckOnly too,
// so a train member is refused before anything merges. A slot with no PR, or
// holding no numeric issue (file backend), has nothing to check.
func (g *gate) stepCloses() (bool, error) {
	keys := g.bundleKeys()
	if g.pr == "" || g.issues == nil || len(keys) == 0 {
		return false, nil
	}
	info, err := g.prView()
	if err != nil {
		if tracker.IsCLIMissing(err) {
			return false, nil
		}
		return g.broke(fmt.Sprintf("could not read %s to check it closes %s: %v", g.pr, refList(keys), err))
	}
	in := closesInput{Slot: g.o.Slot, PR: g.pr, Keys: keys, FinalSlice: g.o.FinalSlice, Issues: g.issues,
		Source: tracker.ClosingSource{Body: info.Body, Title: info.Title, Branch: info.Head}}
	if rep, ok := g.forge.(closingReporter); ok {
		in.Forge = rep
	}
	out := checkCloses(g.ctx, in)
	if out.Verdict == GateNotClosing {
		g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef)
	}
	return g.apply(out)
}

// stepClaim runs checkClaim on the slot's recorded claim.
func (g *gate) stepClaim() (bool, error) {
	if !g.remote || g.issues == nil || g.target.ClaimID == "" {
		return false, nil
	}
	return g.apply(checkClaim(g.ctx, claimInput{Slot: g.o.Slot, ClaimID: g.target.ClaimID, Keys: g.bundleKeys(), Issues: g.issues}))
}

// apply records a step outcome on the gate result and reports whether the gate
// stops.
func (g *gate) apply(out stepOutcome) (bool, error) {
	g.res.Notes = append(g.res.Notes, out.Notes...)
	switch {
	case out.Broke != "":
		return g.broke(out.Broke)
	case out.Verdict != "":
		g.verdict(out.Verdict, out.Msg, out.Hint)
		return true, nil
	}
	return false, nil
}

// releaseClaimLabels releases the claim labels of every issue the merged PR
// closes: the numbers its body closes, plus a key-based tracker's issue named
// in the branch or title instead of a keyword.
func (g *gate) releaseClaimLabels(src tracker.ClosingSource) {
	if g.label == "" || g.issues == nil {
		return
	}
	var keys []tracker.IssueKey
	for _, n := range g.forge.ClosedNumbers(src.Body) {
		keys = append(keys, tracker.NumKey(n))
	}
	for _, key := range g.bundleKeys() {
		if !slices.Contains(keys, key) && g.issues.ClosesIssue(key, src) {
			keys = append(keys, key)
		}
	}
	g.res.Notes = append(g.res.Notes, releaseClaimLabels(g.ctx, releaseInput{Slot: g.o.Slot, Label: g.label, Keys: keys, Issues: g.issues})...)
}

// bundleKeys is the lead issue of the target followed by its bundle members,
// as keys: nil when the target holds no issue, unparsable members skipped.
func (g *gate) bundleKeys() []tracker.IssueKey {
	lead, ok := tracker.ParseKey(g.target.Issue)
	if !ok {
		return nil
	}
	keys := []tracker.IssueKey{lead}
	for _, id := range g.target.Bundle {
		if k, ok := tracker.ParseKey(id); ok && !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	return keys
}
