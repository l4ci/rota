package worker

import (
	"fmt"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/proof"
	"github.com/l4ci/rota/internal/strutil"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
	"slices"
	"strings"
)

// stepAttempt refuses a PR carrying a commit stamped (AttemptTrailer) with an
// attempt the slot no longer holds: a worker whose assignment was reclaimed or
// transferred pushed it, with or without --attempt on its verbs. It judges the
// commits after the branch tip the current attempt inherited, which keep their
// earlier trailers, so a transfer never turns the new holder's PR away. It
// reads the slot, not the queued record, and runs under CheckOnly too.
//
// Anything it cannot tell passes: a slot with no attempt (assigned before the
// fence), an unstamped commit, an inherited tip that is not an ancestor of the
// head (a rebase rewrote it) or a log that fails.
func (runner gateRun) stepAttempt(result landing.GateResult, target GateTarget, refs gateRefsResult) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, target, gateForgeResult{})
	g.headRef, g.baseRef, g.verified = refs.head, refs.base, refs.localHead
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	t := g.target
	if t.Queued || t.External || t.Attempt == "" {
		return finish(false, nil)
	}
	// The base's own commits (a merge of main into the branch brings other
	// workers' stamped commits) are nobody's attempt here: exclude them too.
	rng := []string{g.headRef, "^" + g.baseRef}
	if t.AttemptBase != "" {
		if inherited, err := g.isAncestor(g.root, t.AttemptBase, g.headRef); err != nil || !inherited {
			// A rebase rewrote the inherited tip, or git could not read it: the
			// inherited trailers cannot be told from the stale ones, so judge nothing.
			return finish(false, nil)
		}
		rng = append(rng, "^"+t.AttemptBase)
	}
	out, code := g.git(g.root, append([]string{"log", "--format=%h %(trailers:key=" + AttemptTrailer + ",valueonly,separator=%x20)"}, rng...)...)
	if code != 0 {
		return finish(false, nil)
	}
	for _, line := range strings.Split(out, "\n") {
		sha, stamps, _ := strings.Cut(strings.TrimSpace(line), " ")
		for _, id := range strings.Fields(stamps) {
			if id != t.Attempt {
				g.res.SHA = sha
				g.verdict(landing.GateStaleAttempt, fmt.Sprintf("GATE %s refused — commit %s on %s was pushed by attempt %s, which the slot no longer holds; nothing landed", g.o.Slot, sha, g.branch, id),
					"the assignment was reclaimed or transferred: the holder's own commits are not affected, so have it check that branch, or reassign the issue")
				return finish(true, nil)
			}
		}
	}
	return finish(false, nil)
}

// partialLifted reports whether a partial outcome no longer stands: every
// bundled issue names AC-n criteria and all are met at the gated head: a proof
// mark counts only at that sha, a decision mark (no sha) by a writer. A criterion still open, an item without explicit
// criteria or a read that fails keeps the refusal.
func (g *gateRun) partialLifted(headRef string) (string, bool) {
	keys := g.bundleKeys()
	if g.issues == nil || len(keys) == 0 {
		return "", false
	}
	head, code := g.git(g.root, "rev-parse", headRef)
	if head = strings.TrimSpace(head); code != 0 || head == "" {
		return "", false
	}
	atHead := func(content, sha, check string) (string, bool) {
		if !strutil.SameSHA(head, sha) {
			return "", false
		}
		return proof.Lookup(content, sha, check)
	}
	trust := backlog.TrustOf(g.ctx, g.issues)
	for _, key := range keys {
		is, err := g.issues.Get(g.ctx, key, true)
		if err != nil || !backlog.HasAcceptanceIDs(is.Body) {
			return "", false
		}
		if unmet, err := backlog.UnmetAcceptance(is.Body, is.Comments, atHead, trust); err != nil || len(unmet) > 0 {
			return "", false
		}
	}
	return fmt.Sprintf("PARTIAL-LIFTED %s — every acceptance criterion of %s is met at head", g.o.Slot, refList(keys)), true
}

// stepOutcome refuses a PR whose worker reported partial or failed through
// `worker done --outcome`: it goes to review or relay, never straight to the
// gate. A relay or a transfer is a new brief and clears the outcome; so does the
// worker's next `worker done`. It reads the slot, so a queued PR and an
// external one have no outcome, and it runs under CheckOnly too.
func (runner gateRun) stepOutcome(result landing.GateResult, target GateTarget, forged gateForgeResult, headRef string) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	if g.target.Queued || g.target.External || g.target.Outcome == "" {
		return finish(false, nil)
	}
	if g.target.Outcome == OutcomePartial {
		if note, ok := g.partialLifted(headRef); ok {
			// The lift needs its audit record: no record, no lift.
			var err error
			if !g.o.CheckOnly {
				err = appendGateAudit(g.root, g.e.Now(), "lift-partial", g.o.Slot, note)
			}
			if err == nil {
				g.res.Notes = append(g.res.Notes, note)
				return finish(false, nil)
			}
			g.res.Notes = append(g.res.Notes, fmt.Sprintf("AUDIT-FAILED %s — could not record the lifted partial, so it stays refused: %v", g.o.Slot, err))
		}
	}
	g.gap(landing.GateOutcomeOpen, fmt.Sprintf("GATE %s refused — the worker reported %s; nothing landed", g.o.Slot, g.target.Outcome),
		fmt.Sprintf("read the summary (`rota round status`), then relay slot %s to finish the rest or `rota round transfer` it; a later `worker done --outcome done` lifts this", g.o.Slot))
	return finish(false, nil)
}

// stepProvenance runs the approvals check.
func (runner gateRun) stepProvenance(result landing.GateResult, target GateTarget, forged gateForgeResult, headRef string) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.headRef = headRef
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	failMsg, brokeMsg := g.checkProvenance()
	if brokeMsg != "" {
		return finish(g.broke(brokeMsg))
	}
	if failMsg != "" {
		g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef)
		g.gap(landing.GateProvenanceFail, failMsg, provenanceHint)
	}
	if v, msg, hint := g.requiredSectionsGap(); v != "" {
		g.gap(v, msg, hint)
	}
	return finish(false, nil)
}

// provenanceHint is the fix for a provenance-fail gap; the message names the line.
const provenanceHint = "fix the PR's `## Approvals` lines: cite `orchestrator relay round N` only for a relay logged for that round, never call it the maintainer, or write `settled in brief: <what>`. A relay about a queued PR must be logged against it: send it with `rota worker dispatch <slot> --relay --pr <N>`"

// stepGaps ends the record checks: when any of them found a gap, the gate
// refuses once, listing every one of them.
func (runner gateRun) stepGaps(result landing.GateResult, target GateTarget, forged gateForgeResult) (struct{}, landing.GateResult, bool, error) {
	if len(result.Gaps) == 0 {
		return struct{}{}, result, false, nil
	}
	refuseGateGaps(&result)
	return struct{}{}, result, true, nil
}

// refuseGaps sets the refusal from the recorded gaps. Verdict and Hint are the
// first gap's, so a caller of one gap sees what it always saw; Err appends each
// further gap with its own hint.
func refuseGateGaps(r *landing.GateResult) {
	first := r.Gaps[0]
	r.Verdict, r.Err, r.Hint = first.Verdict, first.Msg, first.Hint
	if len(r.Gaps) > 1 {
		var b strings.Builder
		b.WriteString(first.Msg)
		fmt.Fprintf(&b, "\n%d record gaps in all; fix every one before the next gate run:", len(r.Gaps))
		for _, g := range r.Gaps {
			fmt.Fprintf(&b, "\n  - %s: %s (fix: %s)", g.BlockedBy, g.Msg, g.Hint)
		}
		r.Err = b.String()
	} else {
		r.Gaps = nil
	}
}

// stepBestOf refuses an attempt of a best-of:2 issue until a `rota round pick`
// names its PR, before any fetch, freshness check or bounce. It runs under
// CheckOnly too, so a train member is refused before anything merges. The
// refusal clears itself once the orchestrator picks.
func (runner gateRun) stepBestOf(result landing.GateResult, issue, pr string) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, GateTarget{Issue: issue, PR: pr}, gateForgeResult{})
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	reg, err := LoadRegistry(g.root)
	if err != nil {
		return finish(false, err)
	}
	rec := reg.BestOf(issue)
	if rec == nil || (g.pr != "" && rec.Picked(g.pr)) {
		return finish(false, nil)
	}
	what := "this branch"
	if g.pr != "" {
		what = g.pr
	}
	msg := fmt.Sprintf("GATE %s refused — %s is best-of:2 and no pick names %s; nothing landed", g.o.Slot, tracker.RefOf(rec.Issue), what)
	if rec.Pick != "" {
		msg = fmt.Sprintf("GATE %s refused — %s is best-of:2 and the pick chose %s; this attempt lost; nothing landed", g.o.Slot, tracker.RefOf(rec.Issue), rec.Pick)
	}
	g.verdict(landing.GateBestOfUnpicked, msg, fmt.Sprintf("compare both attempts and run `rota round pick %s --pr <N> --reason-file <f>`", rec.Issue))
	return finish(true, nil)
}

// refList is the keys' refs joined by ", ".
func refList(keys []tracker.IssueKey) string {
	refs := make([]string, len(keys))
	for i, k := range keys {
		refs[i] = k.Ref()
	}
	return strings.Join(refs, ", ")
}

// NeedsCross reports whether a branch at depth needs a review-cross verdict:
// its cross requirement names a harness and the depth reviews anything.
func NeedsCross(res ReviewDepth, depth config.ReviewDepth) bool {
	return res.Cross != "" && res.Cross != CrossNone && depth != config.DepthNone
}

// stepReviewDepth applies ship.review: it resolves the depth for this branch
// from the diff size against the base and the labels of the slot's issue, notes
// it, and refuses a branch that lacks the recorded verdicts that depth needs.
// A recorded FAIL was already refused by stepVerdict. An unreadable policy,
// diff or issue is a refusal too: guessing a depth could pass a risk:high
// branch as light.
func (runner gateRun) stepReviewDepth(result landing.GateResult, target GateTarget, forged gateForgeResult, refs gateRefsResult, verified string) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.headRef, g.baseRef, g.verified = refs.head, refs.base, refs.localHead
	g.verified = verified
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	// An integration branch is judged on its fix commits only (#3096): the
	// members were reviewed on their own PRs before they merged.
	span := g.baseRef + "..." + g.headRef
	reg, err := LoadRegistry(g.root)
	if err != nil { // the integration rules may apply: fail closed
		return finish(g.broke("cannot read the integration records: " + err.Error()))
	}
	intRec, isInt := reg.IntegrationByBranch(g.branch)
	isInt = isInt && !intRec.Landed
	var intHead string
	if isInt {
		var code int
		if intHead, code = g.git(g.root, "rev-parse", g.headRef); code != 0 {
			return finish(g.broke(fmt.Sprintf("git rev-parse %s failed (exit %d)", g.headRef, code)))
		}
		since, err := g.e.ReviewSince(g.root, intRec, intHead)
		if err != nil {
			return finish(g.broke(err.Error()))
		}
		span = since + "..." + g.headRef
	}
	res, err := g.resolveReview(span)
	if err != nil {
		return finish(g.broke(err.Error()))
	}
	depth, why := res.Depth, res.Why
	g.res.Notes = append(g.res.Notes, fmt.Sprintf("REVIEW-DEPTH %s — %s (%s)", g.o.Slot, depth, why))
	if res.Cross != "" {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("REVIEW-CROSS %s — %s (%s)", g.o.Slot, res.Cross, res.CrossWhy))
	}
	for _, l := range res.Lenses {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("REVIEW-LENS %s — %s (%s)", g.o.Slot, l.Name, l.Why))
	}
	if !g.o.CheckReviews {
		return finish(false, nil)
	}
	// A fix PR into an open integration branch is reviewed once, with the
	// branch's fix commits, not on its own (orchestrator ruling, round 55).
	if into, ok := reg.IntegrationByBranch(g.o.Base); ok && !into.Landed {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("REVIEW-DEFERRED %s — its commits are reviewed with the fix commits of %s", g.o.Slot, into.Branch))
		return finish(false, nil)
	}
	if isInt {
		store, err := verdict.LoadStrict(g.root)
		if err != nil {
			g.res.Verdict = landing.GateVerdictBlocked
			return finish(true, err)
		}
		list := store.Branch(g.o.VerdictRepo, g.branch)
		// A commit covers the fixes only with every review the depth asks for
		// (the core kinds, the cross-harness review and each triggered lens)
		// and none of them blocking: the bounce rule the head gets otherwise,
		// applied where the review was recorded, since rota's later merges move
		// the head past it.
		covered := func(sha string) bool {
			if _, bad := verdict.BlockingAtHead(list, sha); bad {
				return false
			}
			if len(verdict.MissingReviews(depth, res.HasSpec, verdict.Recorded(list, sha))) > 0 {
				return false
			}
			if NeedsCross(res, depth) {
				if _, ok := verdict.CrossAtBy(list, sha, res.CrossSatisfiedBy); !ok {
					return false
				}
			}
			for _, l := range res.Lenses {
				if _, ok := verdict.LensAt(list, verdict.LensKind(l.Name), sha); !ok {
					return false
				}
			}
			return true
		}
		// What a covering review needs, named once for the refusal. Nothing
		// at depth none (no cross review, no lens): the fixes need no verdict.
		need := verdict.MissingReviews(depth, res.HasSpec, nil)
		if NeedsCross(res, depth) {
			need = append(need, verdict.ReviewCross)
		}
		for _, l := range res.Lenses {
			need = append(need, verdict.LensKind(l.Name))
		}
		if len(need) == 0 {
			g.res.Notes = append(g.res.Notes, fmt.Sprintf("INTEGRATION-REVIEW %s — review depth %s (%s); the fix commits need no verdict", g.branch, depth, why))
			return finish(false, nil)
		}
		ok, note, err := g.e.integrationReviewed(g.root, intRec, list, intHead, covered)
		if err != nil {
			return finish(g.broke(err.Error()))
		}
		if ok {
			g.res.Notes = append(g.res.Notes, note)
			return finish(false, nil)
		}
		g.res.SHA = strutil.ShortSHA(intHead)
		g.verdict(landing.GateReviewMissing, fmt.Sprintf("GATE %s refused — %s: review depth %s (%s) needs a recorded %s verdict covering them; nothing landed", g.o.Slot, note, depth, why, strings.Join(need, " and ")),
			fmt.Sprintf("run `rota review package %s` (it starts at the first fix) and /rota-review on it, or loosen ship.review", g.branch))
		return finish(true, nil)
	}
	// stepPRMatches resolved the head when the slot has a remote; otherwise resolve it here
	head := g.verified
	if head == "" {
		var code int
		if head, code = g.git(g.root, "rev-parse", g.headRef); code != 0 || strings.TrimSpace(head) == "" {
			return finish(g.broke(fmt.Sprintf("git rev-parse %s failed (exit %d); cannot tell whether the review verdicts are at head", g.headRef, code)))
		}
	}
	store, err := verdict.LoadStrict(g.root)
	if err != nil {
		g.res.Verdict = landing.GateVerdictBlocked
		return finish(true, err)
	}
	list := store.Branch(g.o.VerdictRepo, g.branch)
	have := verdict.Recorded(list, strings.TrimSpace(head))
	missing := verdict.MissingReviews(depth, res.HasSpec, have)
	// A depth of none reviews nothing, so it asks for no cross review either.
	crossMissing := ""
	if NeedsCross(res, depth) {
		if _, ok := verdict.CrossAtBy(list, strings.TrimSpace(head), res.CrossSatisfiedBy); !ok {
			crossMissing = fmt.Sprintf("%s verdict from %s (%s)", verdict.ReviewCross, res.Cross, res.CrossWhy)
		}
	}
	// Each triggered lens needs its own record at head (depth none triggers none).
	var lensMissing []string
	for _, l := range res.Lenses {
		kind := verdict.LensKind(l.Name)
		if _, ok := verdict.LensAt(list, kind, strings.TrimSpace(head)); !ok {
			lensMissing = append(lensMissing, fmt.Sprintf("%s verdict (%s)", kind, l.Why))
		}
	}
	if len(missing) == 0 && crossMissing == "" && len(lensMissing) == 0 {
		return finish(false, nil)
	}
	g.res.SHA, _ = g.git(g.root, "rev-parse", "--short=7", g.headRef)
	var needs []string
	if len(missing) > 0 {
		needs = append(needs, fmt.Sprintf("review depth %s (%s) needs a recorded %s verdict", depth, why, strings.Join(missing, " and ")))
	}
	hint := fmt.Sprintf("run /rota-review on %s (it records the verdicts), or loosen ship.review", g.branch)
	if crossMissing != "" {
		needs = append(needs, "the cross-harness review needs a recorded "+crossMissing)
		hint = fmt.Sprintf("run /rota-review on %s (it records the verdicts and runs the cross-harness reviewer), or add the review:none label to the issue to skip the cross-harness review", g.branch)
	}
	if len(lensMissing) > 0 {
		needs = append(needs, "the review lenses need a recorded "+strings.Join(lensMissing, " and "))
		hint += fmt.Sprintf("; the review lenses need a reviewer per triggered lens recorded by /rota-review on %s, or a looser review.lenses", g.branch)
	}
	g.gap(landing.GateReviewMissing, fmt.Sprintf("GATE %s refused — %s; nothing landed", g.o.Slot, strings.Join(needs, ", and ")), hint)
	return finish(false, nil)
}

// resolveReview resolves the review depth of the gate's branch over span.
func (g *gateRun) resolveReview(span string) (ReviewDepth, error) {
	var prBody func() (string, error)
	if g.pr != "" {
		prBody = g.prBody
	}
	inputs, err := BuildReviewInputs(g.ctx, ReviewInputOptions{
		Config: g.in.cfg, Root: g.root, Dir: g.root, Branch: g.branch,
		Span: span, Git: g.e.Git, Target: &g.target,
		Issues:       func() (ReviewIssues, error) { return g.issues, nil },
		TargetPRBody: prBody,
	})
	if err != nil {
		return ReviewDepth{}, err
	}
	return inputs.Resolve(g.ctx)
}

// stepVerdict applies the shared gate/tick blocking rule, including stale-cross
// relaxation. It runs under CheckOnly too, before a train merges anything.
func (runner gateRun) stepVerdict(result landing.GateResult, target GateTarget, forged gateForgeResult, refs gateRefsResult) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.headRef, g.baseRef = refs.head, refs.base
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	if g.o.NoVerdict {
		return finish(false, nil)
	}
	head, code := g.git(g.root, "rev-parse", "--short", g.headRef) // the head the gate merges
	if head = strings.TrimSpace(head); code != 0 || head == "" {
		return finish(g.broke(fmt.Sprintf("could not resolve gate head %s for verdict checks (exit %d)", g.headRef, code)))
	}
	// Loaded here, after the gate recovered the PR's reports into the store.
	store, err := verdict.LoadStrict(g.root)
	if err != nil {
		g.res.Verdict = landing.GateVerdictBlocked
		return finish(true, err)
	}
	list := store.Branch(g.o.VerdictRepo, g.branch)
	d := verdict.BlockingGate(list, head, false)
	if d.NeedsCrossOff {
		depth, err := g.resolveReview(g.baseRef + "..." + g.headRef)
		if err != nil {
			return finish(g.broke(err.Error()))
		}
		d = verdict.BlockingGate(list, head, depth.CrossOffByLabel())
		if d.Relaxed != nil {
			g.res.Notes = append(g.res.Notes, fmt.Sprintf("REVIEW-CROSS %s — ignored the stale review-cross FAIL recorded at %s (head %s): cross review is off (%s)", g.o.Slot, d.Relaxed.Sha, head, depth.CrossWhy))
		}
	}
	if err := d.Err(g.branch); err != nil {
		g.res.Verdict = landing.GateVerdictBlocked
		return finish(true, err)
	}
	return finish(false, nil)
}

// stepCheckOnly ends a CheckOnly gate with verdict fresh.
func (runner gateRun) stepCheckOnly(result landing.GateResult, target GateTarget, forged gateForgeResult, remote bool, refs gateRefsResult, verified string) (struct{}, landing.GateResult, bool, error) {
	g := runner.operation(result, target, forged)
	g.remote = remote
	g.headRef, g.baseRef, g.verified = refs.head, refs.base, refs.localHead
	g.verified = verified
	finish := func(done bool, err error) (struct{}, landing.GateResult, bool, error) {
		return struct{}{}, *g.res, done, err
	}

	if g.o.CheckOnly {
		if g.o.BehaviourDir != "" {
			if done, err := g.behaviourRun(g.o.BehaviourDir); done || err != nil {
				return finish(true, err)
			}
		}
		// The checked tip is the head the earlier steps (PR match, verdicts,
		// review depth) checked, never the remote ref read again: a fetch since
		// may have moved it to a commit nobody reviewed. Without a remote there
		// is no such head, and the branch is read here.
		head := strings.TrimSpace(g.verified)
		if head == "" {
			head, _ = g.git(g.root, "rev-parse", g.headRef)
		}
		g.res.CheckedHead = head
		g.res.SHA = strutil.ShortSHA(head)
		// A train member says its tier, so the train verifies by the highest.
		if g.o.BehaviourDir == "" {
			tier := g.classify(g.baseRef + "..." + head)
			g.res.Tier, g.res.TierReason = string(tier.Tier), tier.Reason
		}
		g.res.Verdict = landing.GateFresh
		return finish(true, nil)
	}
	return finish(false, nil)
}

// checkProvenance cross-checks the PR body's approvals against the slot's
// relay log. It returns the PROVENANCE-FAIL message, or a check-broke message
// when the PR body cannot be read; both "" for pass or skip.
//
// Only a missing forge CLI may skip. Reading the body failing for any other
// reason (not authenticated, rate limited, PR gone, unparseable reply) used
// to be a skip too, in the Python gate as well, so the gate merged without
// having looked at the approvals. That fails open; here it is check-broke.
func (g *gateRun) checkProvenance() (failMsg, brokeMsg string) {
	if g.pr == "" {
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("PROVENANCE-SKIP %s — no recorded PR (or no %s) to read approvals from", g.o.Slot, g.cliName))
		return "", ""
	}
	body, err := g.prBody()
	if err != nil {
		if tracker.IsCLIMissing(err) {
			g.res.Notes = append(g.res.Notes, fmt.Sprintf("PROVENANCE-SKIP %s — no %s to read approvals from", g.o.Slot, g.cliName))
			return "", ""
		}
		return "", fmt.Sprintf("could not read the body of %s to check its approvals: %v", g.pr, err)
	}
	g.sectionBody = &body
	g.target.relays = dedupeRelays(append(slices.Clone(g.target.relays),
		LoadRegistryTolerant(g.root).taskRelays(g.target.Issue, g.branch)...))
	relays, offered, support, err := g.supportApprovals()
	if err != nil {
		// Relay citations fall back to the local relay log with a skip note.
		// An offer citation cannot: the local log never holds one, so it
		// fails, naming the read error (#1883).
		g.res.Notes = append(g.res.Notes, fmt.Sprintf("PROVENANCE-SKIP %s — could not read the issue comments of %s (%v); checked against the local relay log only", g.o.Slot, g.pr, err))
		if msg := CheckApprovals(body, g.target.relays); msg != "" {
			return fmt.Sprintf("PROVENANCE-FAIL %s: %s", g.o.Slot, msg), ""
		}
		if msg := CheckApprovalsOffer(body, g.target.relays, false); msg != "" {
			return fmt.Sprintf("PROVENANCE-FAIL %s: %s (issue read failed: %v)", g.o.Slot, msg, err), ""
		}
		return "", ""
	}
	check := CheckApprovals
	if support {
		check = func(body string, relays []any) string { return CheckApprovalsOffer(body, relays, offered) }
	}
	if msg := check(body, relays); msg != "" {
		return fmt.Sprintf("PROVENANCE-FAIL %s: %s", g.o.Slot, msg), ""
	}
	return "", ""
}

// supportApprovals is the slot's local relay log plus, for a PR whose issue
// carries the support label under round.support, the relays and the offer the
// other machine left as issue comments (#1827). Only writer-trusted authors
// count. support says the PR is one, so the offer citation is enforced; any
// other PR returns the local log alone, with no extra read.
func (g *gateRun) supportApprovals() (relays []any, offered, support bool, err error) {
	relays = g.target.relays
	cfg := g.in.cfg
	key, ok := tracker.ParseKey(g.target.Issue)
	if cfg.Support == "" || cfg.SupportLabel == "" || g.issues == nil || !ok {
		return relays, false, false, nil
	}
	is, err := g.issues.Get(g.ctx, key, true)
	if err != nil {
		return nil, false, false, err
	}
	if !slices.Contains(is.Labels, cfg.SupportLabel) {
		return relays, false, false, nil
	}
	trust := NewMarkerTrust(g.ctx, g.issues)
	relays = append(slices.Clone(relays), RelaysFromComments(g.ctx, trust, is.Comments)...)
	return relays, HasSupportOffer(trust, is.Comments), true, nil
}
