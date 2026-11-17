package worker

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/change"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/harness"
	labeldefs "github.com/l4ci/rota/internal/labels"
	"github.com/l4ci/rota/internal/reviewpolicy"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/tracker"
)

// ReviewSources is where a branch's review depth is read from. The worker gate
// and `rota review depth` (so /rota-ship) both resolve through ResolveReviewDepth,
// so they pick the same depth and print the same reason.
type ReviewSources struct {
	Policy config.ReviewPolicy
	Git    git.Runner
	Dir    string
	Span   string   // base...head
	Labels []string // labels already known, added to the issue's own
	// Issue and Issues read the branch's issue for its labels and its declared
	// files. Issues is nil for a file source, which has none.
	Issue tracker.IssueKey
	// Members are the other issues of a bundled slot (Issue is the lead). Each is
	// read like the lead and the highest depth any of them requires wins.
	Members []tracker.IssueKey
	Issues  ReviewIssues
	// PRBody reads the PR description for its Door line; nil when there is no PR.
	PRBody func() (string, error)
	// Cross is the cross-harness review requirement's settings (#1612); its
	// zero value turns the requirement off. Author is the harness of the slot
	// holding the branch, "" when no slot does.
	Cross  roundcfg.CrossReview
	Author string
	// Pools reads the reviewer accounts with their meters (#3315). Nil is the
	// gate's static view: every account as unread, so the harness is settled by
	// configuration alone and the account by `rota review depth`.
	Pools func(context.Context) (CrossPools, error)
	// Lenses is review.lenses (#2064): the focused reviewers a branch triggers
	// by the files it touches and its issues' labels. Empty triggers none.
	Lenses config.Lenses
	// Change is change.* (#2348): the tier the diff falls in sets the depth.
	// The zero value has no docs paths and no small tier, so it never applies;
	// ChangeErr, a malformed change.*, classifies as full.
	Change    change.Policy
	ChangeErr error
}

// ReviewDepth is a resolved depth with the inputs it was read from.
type ReviewDepth struct {
	// HasSpec and Members identify the review set gathered by BuildReviewInputs.
	HasSpec bool
	Members []tracker.IssueKey
	reviewpolicy.Decision
	Changed int
	Labels  []string
	// Cross is the harness that must record a review-cross verdict, CrossNone
	// when the requirement is skipped, "" when it does not apply. CrossWhy says
	// which label and setting decided it.
	Cross, CrossWhy string
	// CrossAccount is the review.accounts or round.accounts login the review
	// runs on; nil when no account list decided it (round.harnesses alone).
	CrossAccount *CrossAccount
	// CrossAccepts are the harnesses whose review-cross verdict satisfies the
	// requirement when the account, not a label, picked the harness: headroom
	// moves between the review and the gate, so the gate takes any of them.
	CrossAccepts []string
	// Lenses are the review lenses the branch triggers, each with the path or
	// label that did it. None when the depth is none.
	Lenses []config.LensHit
	// Tier is the change tier the diff was put in.
	Tier change.Result
}

// CrossAccount is the login a cross-harness review runs on. Name "" is the
// harness's ambient login. Fallback says the review.accounts entries were not
// usable and a round.accounts one took over.
type CrossAccount struct {
	Kind, Name, Dir string
	Headroom        *float64
	Fallback        bool
}

// ReviewDepthError identifies a failed source setup or read. Guessing a depth
// could pass a risky branch as light, so every failed read is an error.
type ReviewDepthError struct {
	Stage string // "issue-source" setup, or "diff", "issue", "pr" reads
	Msg   string
	Err   error
}

func (e *ReviewDepthError) Error() string { return e.Msg }
func (e *ReviewDepthError) Unwrap() error { return e.Err }

// ResolveReviewDepth reads the diff, the issue and the PR body and applies the
// policy. The issue is read only when the policy can use it: for a label to
// match or for a declared file set to drift from.
func ResolveReviewDepth(ctx context.Context, s ReviewSources) (ReviewDepth, error) {
	changed, err := git.ChangedLines(ctx, s.Git, s.Dir, s.Span)
	if err != nil {
		return ReviewDepth{}, &ReviewDepthError{"diff", fmt.Sprintf("could not diff %s to pick the review depth", s.Span), err}
	}
	files, err := git.DiffFiles(ctx, s.Git, s.Dir, s.Span)
	if err != nil {
		return ReviewDepth{}, &ReviewDepthError{"diff", fmt.Sprintf("could not list the files of %s to pick the review depth", s.Span), err}
	}
	in := reviewpolicy.Input{Changed: changed, Files: files, Labels: slices.Clone(s.Labels)}
	// The PR body is read at most once, for the tier's Door line and the policy's.
	prRead := false
	var prBody string
	var prErr error
	readPR := s.PRBody
	if readPR != nil {
		readPR = func() (string, error) {
			if !prRead {
				prRead = true
				prBody, prErr = s.PRBody()
			}
			return prBody, prErr
		}
	}
	var tier change.Result
	if s.ChangeErr != nil || len(s.Change.DocsPaths) > 0 || len(s.Change.RiskyPaths) > 0 || s.Change.SmallBelow > 0 {
		tier = ClassifyChange(ctx, ChangeSources{Policy: s.Change, PolicyErr: s.ChangeErr, Git: s.Git, Dir: s.Dir, Span: s.Span, PRBody: readPR})
		in.Tier, in.Risky = tier.Tier, tier.Risky
	}
	needsIssue := s.Policy.NeedsIssue()
	if in.Tier == change.TierDocs {
		needsIssue = len(s.Policy.Labels) > 0 // no drift to read the issue for
	}
	decide := func(in reviewpolicy.Input) reviewpolicy.Decision { return reviewpolicy.Decide(s.Policy, in) }
	// Each issue's own labels decide its cross requirement, as they do its
	// depth: one issue's review:none must not skip another's.
	labelSets := [][]string{in.Labels}
	if s.Issues != nil && s.Issue != "" && (needsIssue || s.Cross.Active() || s.Lenses.UsesLabels()) {
		keys := append([]tracker.IssueKey{s.Issue}, s.Members...)
		var got []tracker.Issue
		var bodies []string
		for _, key := range keys {
			is, err := s.Issues.Get(ctx, key, false)
			if err != nil {
				return ReviewDepth{}, &ReviewDepthError{"issue", fmt.Sprintf("could not read %s for its labels and files to pick the review depth: %v", key.Ref(), err), err}
			}
			got = append(got, is)
			bodies = append(bodies, is.Body)
			for _, l := range is.Labels {
				if !slices.Contains(in.Labels, l) {
					in.Labels = append(in.Labels, l)
				}
			}
		}
		in.IssueBody = strings.Join(bodies, "\n\n")
		labelSets = labelSets[:0]
		for _, is := range got {
			labelSets = append(labelSets, append(slices.Clone(s.Labels), is.Labels...))
		}
		if len(got) > 1 {
			// One decision per bundled issue, the highest wins: a union of
			// labels would let a `none` on one issue beat another's default.
			// The PR body is read below, once, for every decision.
			decide = func(in reviewpolicy.Input) reviewpolicy.Decision {
				var best reviewpolicy.Decision
				for i, is := range got {
					one := in
					one.Labels = slices.Clone(s.Labels)
					for _, l := range is.Labels {
						if !slices.Contains(one.Labels, l) {
							one.Labels = append(one.Labels, l)
						}
					}
					if d := reviewpolicy.Decide(s.Policy, one); i == 0 || d.Depth.Outranks(best.Depth) {
						best = d
					}
				}
				return best
			}
		}
	}
	// A body read only for the tier failed closed there (tier full); only the
	// policy's own need for the Door line makes an unreadable body an error.
	if readPR != nil && (s.Policy.MayBeBelowFull() || prRead) {
		body, err := readPR()
		if err != nil && !tracker.IsCLIMissing(err) && s.Policy.MayBeBelowFull() {
			return ReviewDepth{}, &ReviewDepthError{"pr", fmt.Sprintf("could not read the PR body for its Door line to pick the review depth: %v", err), err}
		}
		if err == nil {
			in.PRBody = body
		}
	}
	out := ReviewDepth{Decision: decide(in), Changed: changed, Labels: in.Labels, Tier: tier}
	// A depth of none reviews nothing, so no lens runs either. in.Labels is the
	// union of the lead's and every member's, so any bundled issue can trigger.
	if out.Depth != config.DepthNone {
		out.Lenses = reviewpolicy.MatchLenses(s.Lenses, files, in.Labels)
	}
	author, _ := harness.ResolveAuthor(harness.AuthorSources{
		Slot: s.Author, Labels: labelSets[0], Project: s.Cross.DefaultKind,
	})
	var pools *CrossPools
	for _, labels := range labelSets {
		if pools == nil && s.Cross.Active() && len(s.Cross.Review) > 0 {
			p, err := readCrossPools(ctx, s)
			if err != nil {
				return ReviewDepth{}, &ReviewDepthError{"accounts", fmt.Sprintf("could not read the reviewer accounts to pick the cross-harness reviewer: %v", err), err}
			}
			pools = &p
		}
		if r := resolveCross(s.Cross, labels, author, pools); crossRank(r.Harness) > crossRank(out.Cross) {
			out.Cross, out.CrossWhy, out.CrossAccount, out.CrossAccepts = r.Harness, r.Why, r.Account, r.Accepts
		}
	}
	return out, nil
}

// readCrossPools reads the reviewer accounts through s.Pools, or builds the
// static view (every account unread) when the caller has no meters.
func readCrossPools(ctx context.Context, s ReviewSources) (CrossPools, error) {
	if s.Pools != nil {
		return s.Pools(ctx)
	}
	return StaticCrossPools(s.Cross), nil
}

// StaticCrossPools is the reviewer pool with no meter readings: review.accounts
// entries, then the round.accounts entries of every allowed harness (the
// harness's ambient login when it has none). Rank is round.harnesses order.
func StaticCrossPools(c roundcfg.CrossReview) CrossPools {
	rank := func(kind string) int {
		if i := slices.Index(c.Harnesses, kind); i >= 0 {
			return i
		}
		return len(c.Harnesses)
	}
	entry := func(a config.Account, order int) PoolEntry {
		return PoolEntry{Kind: a.Kind, Name: a.Name, Rank: rank(a.Kind), Order: order,
			Meter: Meter{Name: a.Name, ConfigDir: a.Dir, Verdict: VerdictUnknown}}
	}
	var p CrossPools
	for i, a := range c.Review {
		p.Review = append(p.Review, entry(a, i))
	}
	kinds := c.Harnesses
	if len(kinds) == 0 {
		for _, a := range c.Main {
			if !slices.Contains(kinds, a.Kind) {
				kinds = append(kinds, a.Kind)
			}
		}
	}
	for _, k := range kinds {
		// A harness with only review accounts has no worker login to fall back to.
		if slices.ContainsFunc(c.Review, func(a config.Account) bool { return a.Kind == k }) && !slices.ContainsFunc(c.Main, func(a config.Account) bool { return a.Kind == k }) {
			continue
		}
		n := len(p.Main)
		for _, a := range c.Main {
			if a.Kind == k {
				p.Main = append(p.Main, entry(a, len(p.Main)))
			}
		}
		if len(p.Main) == n {
			p.Main = append(p.Main, entry(config.Account{Kind: k}, len(p.Main)))
		}
	}
	return p
}

// CrossSatisfiedBy reports whether a review-cross verdict recorded by harness
// meets the requirement: the harness it names, or any CrossAccepts one.
func (r ReviewDepth) CrossSatisfiedBy(harness string) bool {
	return harness == r.Cross || slices.Contains(r.CrossAccepts, harness)
}

// CrossNone is ReviewDepth.Cross when a PR that would need a cross-harness
// review gets none: review:none, or no other harness to run it.
const CrossNone = "none"

// crossWhyReviewNone is CrossWhy when the issue's review:none label skipped the cross review.
const crossWhyReviewNone = "label review:none"

// CrossOffByLabel reports whether the cross review is skipped because the
// issue carries review:none, the only reason that relaxes a stale cross FAIL.
func (r ReviewDepth) CrossOffByLabel() bool { return r.CrossWhy == crossWhyReviewNone }

// crossReviewer reports whether kind can run a cross-harness review.
func crossReviewer(kind string) bool {
	return harness.Valid(kind) && harness.ResolveReviewer(kind).CrossReviewer()
}

// crossRank orders cross results for a bundle: a harness beats none beats "".
func crossRank(c string) int {
	switch c {
	case "":
		return 0
	case CrossNone:
		return 1
	}
	return 2
}

// crossResult is one issue's cross-harness requirement.
type crossResult struct {
	Harness, Why string
	Account      *CrossAccount
	Accepts      []string
}

// resolveCross is one issue's cross-harness requirement (#1612): the reviewer
// harness, CrossNone, or "" when none applies. A label in review.crossLabels
// triggers it; review:none skips it and beats review:<harness>, which picks the
// reviewer; otherwise it is the first reviewer in round.harnesses that is not
// the author's harness. A review:<x> naming no reviewer is not a routing label.
// With review.accounts set (#3315, pools non-nil) the account list decides
// instead: see crossByAccount.
func resolveCross(c roundcfg.CrossReview, labels []string, author string, pools *CrossPools) crossResult {
	if !c.Active() {
		return crossResult{}
	}
	var trigger, route string
	for _, l := range labels {
		if trigger == "" && slices.Contains(c.Labels, l) {
			trigger = l
		}
		if v, ok := strings.CutPrefix(l, labeldefs.ReviewPrefix); ok && route != CrossNone && (v == CrossNone || crossReviewer(v)) {
			route = v
		}
	}
	switch {
	case trigger == "" && route == "":
		return crossResult{}
	case trigger == "":
		return crossResult{Harness: CrossNone, Why: fmt.Sprintf("label review:%s, but the issue has no label in review.crossLabels (%s)", route, strings.Join(c.Labels, ", "))}
	case route == CrossNone:
		return crossResult{Harness: CrossNone, Why: crossWhyReviewNone}
	}
	if pools != nil {
		return crossByAccount(c, trigger, route, author, *pools)
	}
	if route != "" {
		return crossResult{Harness: route, Why: fmt.Sprintf("label %s; label review:%s", trigger, route)}
	}
	for _, k := range c.Harnesses {
		if k != author && crossReviewer(k) {
			return crossResult{Harness: k, Why: fmt.Sprintf("label %s; first of round.harnesses other than the author's %s", trigger, author)}
		}
	}
	return crossResult{Harness: CrossNone, Why: fmt.Sprintf("label %s, but round.harnesses lists no reviewer harness other than the author's %s", trigger, author)}
}

// crossByAccount is resolveCross when review.accounts is set: ChooseCross over
// the pools. A label-picked harness is fixed. Otherwise the account picks the
// harness, so the verdict of any eligible harness satisfies the gate (the
// headroom that decided it moves between the review and the gate).
func crossByAccount(c roundcfg.CrossReview, trigger, route, author string, pools CrossPools) crossResult {
	ch := ChooseCross(CrossRequest{Author: author, Kind: route, Pools: pools})
	how := "label " + trigger
	if route != "" {
		how += "; label review:" + route
	}
	if !ch.OK {
		return crossResult{Harness: CrossNone, Why: how + "; " + ch.Why}
	}
	e := ch.Account
	key := config.RoundAccountsKey
	if ch.Review {
		key = config.ReviewAccountsKey
	}
	why := fmt.Sprintf("%s; %s %s", how, key, accountName(e))
	if e.Meter.Headroom != nil {
		why += fmt.Sprintf(" (headroom %g%%)", *e.Meter.Headroom)
	}
	if ch.Why != "" {
		why += "; " + ch.Why
	}
	res := crossResult{Harness: e.Kind, Why: why,
		Account: &CrossAccount{Kind: e.Kind, Name: e.Name, Dir: e.Meter.ConfigDir, Headroom: e.Meter.Headroom, Fallback: ch.Fallback}}
	if route == "" {
		for _, p := range append(slices.Clone(pools.Review), pools.Main...) {
			if p.Kind != author && crossReviewer(p.Kind) && !slices.Contains(res.Accepts, p.Kind) {
				res.Accepts = append(res.Accepts, p.Kind)
			}
		}
	}
	return res
}

// DoorNote explains why a depth below full has not seen the PR's Door line: there
// was no PR to read, or no forge to read one from. It is empty when the line was
// read, when the depth is full, when a label mapped to none (final, so the line
// cannot change it) or when the policy cannot come out below full.
func DoorNote(policy config.ReviewPolicy, res ReviewDepth, prFound, noForge bool) string {
	labelNone := res.Signal == reviewpolicy.SignalLabel && res.Depth == config.DepthNone
	if prFound || res.Depth == config.DepthFull || labelNone || !policy.MayBeBelowFull() {
		return ""
	}
	if noForge {
		return "; no forge to read, so no Door line was read"
	}
	return "; no PR yet, so no Door line was read"
}
