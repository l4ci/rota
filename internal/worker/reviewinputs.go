package worker

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/ledger"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
)

// ReviewIssues is the issue read the depth resolver needs.
type ReviewIssues interface {
	Get(context.Context, tracker.IssueKey, bool) (tracker.Issue, error)
}

// ReviewInputOptions names the caller-owned boundaries. Target is authoritative
// for a gate, whose target may have changed after adopting an external PR. A
// preview leaves it nil to find the branch's slot or queued record itself.
type ReviewInputOptions struct {
	Config                  GateConfig
	Root, Dir, Branch, Span string
	Git                     git.Runner
	Target                  *GateTarget
	Labels                  []string
	Issues                  func() (ReviewIssues, error)
	// TargetPRBody uses the gate's already selected PR and its cached body.
	TargetPRBody func() (string, error)
	// BranchForge discovers an open PR before there is a gate target. A local
	// forge or missing CLI means no PR; other errors follow the depth policy.
	BranchForge func() (tracker.Forge, error)
}

// ReviewInputs holds the shared sources plus review-set metadata and the
// preview's explanation of an absent PR. Build it once per depth resolution.
type ReviewInputs struct {
	Sources          ReviewSources
	HasSpec          bool
	prFound, noForge bool
}

// BuildReviewInputs gathers the policy, author, issue, bundle and PR sources
// for gates, review previews and verdict nudges. Issue reads remain lazy: the
// resolver decides whether the policy, change tier, cross review or lenses
// actually need their labels and declared files.
func BuildReviewInputs(ctx context.Context, o ReviewInputOptions) (*ReviewInputs, error) {
	policy, err := o.Config.ReviewPolicy()
	if err != nil {
		return nil, fmt.Errorf("%w; run rota config check", err)
	}
	cross, err := o.Config.CrossReview()
	if err != nil {
		return nil, fmt.Errorf("%w; run rota config check", err)
	}
	lenses, err := o.Config.Lenses()
	if err != nil {
		return nil, fmt.Errorf("%w; run rota config check", err)
	}
	root := o.Root
	var target GateTarget
	if o.Target != nil {
		target = *o.Target
	} else {
		reg := LoadRegistryTolerant(root)
		if !reg.Exists {
			if common, err := rotastate.CommonDirVia(ctx, o.Git, root); err == nil {
				if main, ok := rotastate.MainCheckout(common); ok && main != root {
					root = main
					reg = LoadRegistryTolerant(root)
				}
			}
		}
		if key, ok := BranchIssue(o.Branch); ok {
			target.Issue = key.String()
		}
		sl, q := reg.BranchHolder(o.Branch)
		if sl != nil {
			target = gateSlot(sl)
		}
		if q != nil {
			if sl == nil {
				target = gateQueued(*q)
			} else if len(target.Bundle) == 0 {
				target.Bundle = q.Bundle
			}
		}
	}
	in := &ReviewInputs{HasSpec: verdict.HasSpec(target.Issue)}
	src := &in.Sources
	*src = ReviewSources{Policy: policy, Git: o.Git, Dir: o.Dir, Span: o.Span,
		Labels: slices.Clone(o.Labels), Cross: cross, Author: target.Kind, Lenses: lenses}
	src.Change, src.ChangeErr = o.Config.ChangePolicy()
	author := harness.AuthorSources{Slot: target.Kind}
	if author.Slot == "" {
		es, _ := ledger.Load(root)
		if e := ledger.LatestAssign(es, ledger.AssignQuery{Issue: target.Issue, RequireHarness: true}); e != nil {
			author.Assigned = e.Harness
		}
	}
	if author.Slot != "" || author.Assigned != "" {
		src.Author, _ = harness.ResolveAuthor(author)
	}
	for _, id := range target.Bundle {
		if key, ok := tracker.ParseKey(id); ok {
			src.Members = append(src.Members, key)
		}
	}
	if key, ok := tracker.ParseKey(target.Issue); ok && o.Issues != nil && (policy.NeedsIssue() || cross.Active() || lenses.UsesLabels()) {
		issues, err := o.Issues()
		if err != nil {
			return nil, err
		}
		if _, none := issues.(tracker.NoIssues); !none {
			src.Issue, src.Issues = key, issues
		}
	}
	src.PRBody = o.TargetPRBody
	tiered := src.ChangeErr == nil && (len(src.Change.DocsPaths) > 0 || src.Change.SmallBelow > 0)
	if src.PRBody == nil && o.BranchForge != nil && (policy.MayBeBelowFull() || tiered) {
		f, err := o.BranchForge()
		switch {
		case err == nil:
			src.PRBody = func() (string, error) {
				prs, err := f.OpenPRs(ctx)
				if err != nil {
					return "", err
				}
				for _, pr := range prs {
					if pr.Branch == o.Branch {
						in.prFound = true
						return pr.Body, nil
					}
				}
				return "", nil
			}
		case errors.Is(err, tracker.ErrLocalForge) || tracker.IsCLIMissing(err):
			in.noForge = true
		case !policy.MayBeBelowFull():
			src.PRBody = func() (string, error) { return "", err }
		default:
			return nil, fmt.Errorf("could not reach the forge to read the PR body for its Door line to pick the review depth: %w", err)
		}
	}
	return in, nil
}

// Resolve applies the existing depth policy and carries the same issue and
// bundle identity into review-set checks and preview output.
func (in *ReviewInputs) Resolve(ctx context.Context) (ReviewDepth, error) {
	res, err := ResolveReviewDepth(ctx, in.Sources)
	res.HasSpec, res.Members = in.HasSpec, slices.Clone(in.Sources.Members)
	return res, err
}

// PreviewWhy adds only the preview's missing-PR explanation to the shared reason.
func (in *ReviewInputs) PreviewWhy(res ReviewDepth) string {
	return res.Why + DoorNote(in.Sources.Policy, res, in.prFound, in.noForge)
}
