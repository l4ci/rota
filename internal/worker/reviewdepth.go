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
	config.ReviewDecision
	Changed int
	Labels  []string
	// Cross is the harness that must record a review-cross verdict, CrossNone
	// when the requirement is skipped, "" when it does not apply. CrossWhy says
	// which label and setting decided it.
	Cross, CrossWhy string
	// Lenses are the review lenses the branch triggers, each with the path or
	// label that did it. None when the depth is none.
	Lenses []config.LensHit
	// Tier is the change tier the diff was put in.
	Tier change.Result
}

// ReviewDepthError says which read failed. Guessing a depth could pass a risky
// branch as light, so every failed read is an error.
type ReviewDepthError struct {
	Stage string // "diff", "issue" or "pr"
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
	in := config.ReviewInput{Changed: changed, Files: files, Labels: slices.Clone(s.Labels)}
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
	decide := s.Policy.Decide
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
			decide = func(in config.ReviewInput) config.ReviewDecision {
				var best config.ReviewDecision
				for i, is := range got {
					one := in
					one.Labels = slices.Clone(s.Labels)
					for _, l := range is.Labels {
						if !slices.Contains(one.Labels, l) {
							one.Labels = append(one.Labels, l)
						}
					}
					if d := s.Policy.Decide(one); i == 0 || d.Depth.Outranks(best.Depth) {
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
	out := ReviewDepth{ReviewDecision: decide(in), Changed: changed, Labels: in.Labels, Tier: tier}
	// A depth of none reviews nothing, so no lens runs either. in.Labels is the
	// union of the lead's and every member's, so any bundled issue can trigger.
	if out.Depth != config.DepthNone {
		out.Lenses = s.Lenses.Match(files, in.Labels)
	}
	author, _ := harness.ResolveAuthor(harness.AuthorSources{
		Slot: s.Author, Labels: labelSets[0], Project: s.Cross.DefaultKind,
	})
	for _, labels := range labelSets {
		if c, why := resolveCross(s.Cross, labels, author); crossRank(c) > crossRank(out.Cross) {
			out.Cross, out.CrossWhy = c, why
		}
	}
	return out, nil
}

// CrossNone is ReviewDepth.Cross when a PR that would need a cross-harness
// review gets none: review:none, or no other harness to run it.
const CrossNone = "none"

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

// resolveCross is one issue's cross-harness requirement (#1612): the reviewer
// harness, CrossNone, or "" when none applies. A label in review.crossLabels
// triggers it; review:none skips it and beats review:<harness>, which picks the
// reviewer; otherwise it is the first reviewer in round.harnesses that is not
// the author's harness. A review:<x> naming no reviewer is not a routing label.
func resolveCross(c roundcfg.CrossReview, labels []string, author string) (string, string) {
	if !c.Active() {
		return "", ""
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
		return "", ""
	case trigger == "":
		return CrossNone, fmt.Sprintf("label review:%s, but the issue has no label in review.crossLabels (%s)", route, strings.Join(c.Labels, ", "))
	case route == CrossNone:
		return CrossNone, "label review:none"
	case route != "":
		return route, fmt.Sprintf("label %s; label review:%s", trigger, route)
	}
	for _, k := range c.Harnesses {
		if k != author && crossReviewer(k) {
			return k, fmt.Sprintf("label %s; first of round.harnesses other than the author's %s", trigger, author)
		}
	}
	return CrossNone, fmt.Sprintf("label %s, but round.harnesses lists no reviewer harness other than the author's %s", trigger, author)
}

// DoorNote explains why a depth below full has not seen the PR's Door line: there
// was no PR to read, or no forge to read one from. It is empty when the line was
// read, when the depth is full, when a label mapped to none (final, so the line
// cannot change it) or when the policy cannot come out below full.
func DoorNote(policy config.ReviewPolicy, res ReviewDepth, prFound, noForge bool) string {
	labelNone := res.Signal == config.SignalLabel && res.Depth == config.DepthNone
	if prFound || res.Depth == config.DepthFull || labelNone || !policy.MayBeBelowFull() {
		return ""
	}
	if noForge {
		return "; no forge to read, so no Door line was read"
	}
	return "; no PR yet, so no Door line was read"
}
