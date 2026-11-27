// Package reviewpolicy resolves the review depth of a branch from a typed
// config.ReviewPolicy and the text around the diff: labels, the PR body's Door
// line and the issue body's declared files. config owns the settings; the body
// grammar lives in itembody.
package reviewpolicy

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/l4ci/rota/internal/change"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/gopath"
	"github.com/l4ci/rota/internal/itembody"
	"github.com/l4ci/rota/internal/pathglob"
)

// Signals name which rule decided a review depth.
const (
	SignalLabel      = "label"
	SignalDoor       = "door"
	SignalScopeDrift = "scope-drift"
	SignalPath       = "path"
	SignalSize       = "size"
	SignalDefault    = "default"
	SignalTier       = "tier"
	SignalRisky      = "risky"
)

// Input is everything a depth is resolved from. Changed is the diff's
// changed-line count (negative: unknown). Files are the changed paths (nil:
// unknown). Door is a one-way Door line in the PR body, read once by the caller
// (worker.DoorLine).
// IssueBody is the body of the branch's issue ("" when there is none). Tier
// is the change tier of the diff ("" when tiers do not apply).
type Input struct {
	Changed int
	Labels  []string
	// HasLabel is supplied for tracker-read labels; nil keeps policy-only inputs exact.
	HasLabel  func([]string, string) bool `json:"-"`
	Files     []string
	Door      bool
	IssueBody string
	// IssueBodyUnknown: the issue body was never read (no issue source, no
	// issue key), so "" in IssueBody means unread, not "declares no files".
	IssueBodyUnknown bool
	Tier             change.Tier
	// Risky: a change.riskyPaths glob matched a changed file.
	Risky bool
}

// Decision is a resolved depth, the signal that decided it and why.
type Decision struct {
	Depth  config.ReviewDepth
	Signal string
	Why    string
	// DriftUnchecked is set when the scope-drift rule applied but the issue body
	// was unknown, so drift was not checked. It is not a "no drift" result.
	DriftUnchecked string
}

// Decide picks the depth for a branch and says which signal decided it. A label
// override beats the size rule; when several labels match, the strictest wins.
// A label that maps to none is final. Otherwise the base depth (label, size or
// default) is raised, never lowered, by a one-way Door line in the PR body, a
// diff outside the issue's declared files, or a changed path matching a paths
// glob. The change tier (#2348) sets the base before the raises: a docs-tier
// diff is none and a small one caps the base and scope drift at light. A label
// still decides the base, a docs-tier diff never counts as drift, and the Door
// line and the paths globs still raise any tier.
func Decide(p config.ReviewPolicy, in Input) Decision {
	base := baseDecision(p, in.Changed, in.Labels, in.HasLabel)
	if base.Signal == SignalLabel && base.Depth == config.DepthNone {
		return base
	}
	labelled := base.Signal == SignalLabel
	driftCap := config.DepthFull
	switch {
	case in.Tier == change.TierDocs && !labelled:
		base = Decision{Depth: config.DepthNone, Signal: SignalTier, Why: "docs-only change (tier docs)"}
	case in.Tier == change.TierSmall:
		driftCap = config.DepthLight
		if !labelled && base.Depth.Outranks(config.DepthLight) {
			base = Decision{Depth: config.DepthLight, Signal: SignalTier, Why: "small change (tier small)"}
		}
	}
	best := base
	raise := func(d config.ReviewDepth, signal, why string) {
		if d.Outranks(best.Depth) {
			best = Decision{Depth: d, Signal: signal, Why: why}
		}
	}
	if in.Door {
		raise(config.DepthFull, SignalDoor, change.DoorReason)
	}
	if in.Risky {
		raise(config.DepthFull, SignalRisky, "diff touches change.riskyPaths")
	}
	unchecked := ""
	if driftApplies(p, in.Tier) && in.IssueBodyUnknown {
		unchecked = "issue body not read, so scope drift was not checked"
	} else if driftApplies(p, in.Tier) {
		if drift := scopeDrift(in.IssueBody, in.Files, p.EffectiveSupportPaths()); len(drift) > 0 {
			d := config.DepthFull
			if d.Outranks(driftCap) {
				d = driftCap
			}
			raise(d, SignalScopeDrift, "diff touches files the issue never named: "+strings.Join(drift, ", "))
		}
	}
	globs := make([]string, 0, len(p.Paths))
	for g := range p.Paths {
		globs = append(globs, g)
	}
	slices.Sort(globs)
	for _, g := range globs {
		for _, f := range in.Files {
			if pathglob.MatchEntry(pathglob.Recursive, g, f) {
				raise(p.Paths[g], SignalPath, fmt.Sprintf("%s matches paths %s (%s)", f, g, p.Paths[g]))
				break
			}
		}
	}
	best.DriftUnchecked = unchecked
	return best
}

// NeedsIssue reports whether the issue can change the depth of a change of this
// tier: a label can match at any tier, a declared file set to drift from only
// where scope drift applies (never for docs). Decide and the worker's issue
// read share this one rule.
func NeedsIssue(p config.ReviewPolicy, tier change.Tier) bool {
	return len(p.Labels) > 0 || (driftApplies(p, tier) && p.MayBeBelowFull())
}

// driftApplies is the one statement of "a docs-tier change needs no scope-drift
// check".
func driftApplies(p config.ReviewPolicy, tier change.Tier) bool {
	return !p.NoScopeDrift && tier != change.TierDocs
}

// baseDecision is the depth from labels, then the size rule, then the default.
func baseDecision(p config.ReviewPolicy, changed int, labels []string, match func([]string, string) bool) Decision {
	var hit []string
	for label := range p.Labels {
		if labelPresent(match, labels, label) {
			hit = append(hit, label)
		}
	}
	if len(hit) > 0 {
		slices.Sort(hit)
		best := hit[0]
		for _, l := range hit[1:] {
			if p.Labels[l].Outranks(p.Labels[best]) {
				best = l
			}
		}
		return Decision{Depth: p.Labels[best], Signal: SignalLabel, Why: fmt.Sprintf("label %s", best)}
	}
	if p.Default == config.DepthFull && p.LightBelow > 0 && changed >= 0 && changed < p.LightBelow {
		return Decision{Depth: config.DepthLight, Signal: SignalSize, Why: fmt.Sprintf("%d changed lines, under lightBelow %d", changed, p.LightBelow)}
	}
	if p.Legacy() != "" {
		return Decision{Depth: p.Default, Signal: SignalDefault, Why: fmt.Sprintf("ship.review is %s", p.Legacy())}
	}
	return Decision{Depth: p.Default, Signal: SignalDefault, Why: "default depth"}
}

// sitsBesideDeclared reports whether a _test.go file lives in the directory of
// a declared file: its tests belong to the declared code. A declared directory
// has no sibling files; its own files are covered by the declared set.
func sitsBesideDeclared(file string, allowed []string) bool {
	if !gopath.IsTestGo(file) {
		return false
	}
	dir := path.Dir(file)
	return slices.ContainsFunc(allowed, func(a string) bool {
		// only a file entry (it has an extension) names its own directory
		return path.Ext(a) != "" && !strings.ContainsAny(a, "*?[") && path.Dir(a) == dir
	})
}

// scopeDrift lists the changed files the issue body never named. The declared
// set is its "## Files" entries plus the path-like (slash or glob) entries of
// "## Touches" and "## Pointers". A trailing /** names a directory, as in the
// paths map, and so does a bare directory. A support file (a support glob
// match, or a _test.go file beside a declared file) is not drift. An issue
// that declares none has no set to drift from.
func scopeDrift(issueBody string, files, support []string) []string {
	var allowed []string
	for _, sec := range itembody.Sections(issueBody, itembody.FilesHeadRe) {
		allowed = append(allowed, itembody.BulletPaths(sec, false)...)
	}
	for _, sec := range itembody.Sections(issueBody, itembody.TouchesHeadRe) {
		allowed = append(allowed, itembody.BulletPaths(sec, true)...)
	}
	for _, sec := range itembody.Sections(issueBody, itembody.PointersHeadRe) {
		allowed = append(allowed, itembody.BulletPaths(sec, true)...)
	}
	if len(allowed) == 0 {
		return nil
	}
	var out []string
	for _, f := range files {
		if slices.ContainsFunc(allowed, func(a string) bool { return pathglob.MatchEntry(pathglob.Recursive, a, f) }) ||
			slices.ContainsFunc(support, func(g string) bool { return pathglob.MatchUnder(g, f) }) ||
			sitsBesideDeclared(f, allowed) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// MatchLenses lists the lenses files or labels trigger, in LensNames order, each
// with the first path (else label) that did it.
func MatchLenses(l config.Lenses, files, labels []string, matchers ...func([]string, string) bool) []config.LensHit {
	var match func([]string, string) bool
	if len(matchers) > 0 {
		match = matchers[0]
	}
	var hits []config.LensHit
	for _, name := range config.LensNames {
		lens, ok := l[name]
		if !ok {
			continue
		}
		why := ""
	paths:
		for _, g := range lens.Paths {
			for _, f := range files {
				if pathglob.MatchEntry(pathglob.Recursive, g, f) {
					why = "path " + f
					break paths
				}
			}
		}
		if why == "" {
			for _, lb := range labels {
				if labelPresent(match, lens.Labels, lb) {
					why = "label " + lb
					break
				}
			}
		}
		if why != "" {
			hits = append(hits, config.LensHit{Name: name, Why: why})
		}
	}
	return hits
}

// Policy lists without tracker provenance retain their exact matching behavior.
func labelPresent(match func([]string, string) bool, names []string, want string) bool {
	if match != nil {
		return match(names, want)
	}
	return slices.Contains(names, want)
}
