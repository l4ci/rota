package config

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/change"
	"github.com/l4ci/rota/internal/gopath"
	"github.com/l4ci/rota/internal/itembody"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/overlap"
)

// ReviewDepth is how much review a branch gets before it ships: full runs
// /rota-review as is, light runs only its Spec reviewer (its Standards reviewer when the
// branch has no spec), none skips it.
type ReviewDepth string

const (
	DepthFull  ReviewDepth = "full"
	DepthLight ReviewDepth = "light"
	DepthNone  ReviewDepth = "none"
)

// ReviewKey is the config key the policy lives under.
const ReviewKey = "ship.review"

func (d ReviewDepth) valid() bool { return d == DepthFull || d == DepthLight || d == DepthNone }

// rank orders depths so the strictest label wins: none < light < full.
func (d ReviewDepth) rank() int {
	switch d {
	case DepthFull:
		return 2
	case DepthLight:
		return 1
	}
	return 0
}

// Outranks reports whether d is strictly stricter than o: none < light < full.
func (d ReviewDepth) Outranks(o ReviewDepth) bool { return d.rank() > o.rank() }

// ReviewPolicy is ship.review in object form. Default applies when no label
// and no size rule does. LightBelow, when above 0, downgrades a Default of full
// to light for a diff of fewer changed lines. Labels override both. Paths maps
// a glob to the least depth a changed path matching it gets. NoScopeDrift turns
// off the rule that a diff outside the issue's declared files resolves full.
// SupportPaths lists the globs of files that never count as drift; nil means
// DefaultSupportPaths, an empty list means every undeclared file counts.
type ReviewPolicy struct {
	Default      ReviewDepth
	LightBelow   int
	Labels       map[string]ReviewDepth
	Paths        map[string]ReviewDepth
	NoScopeDrift bool
	SupportPaths []string
	legacy       string // "true" or "false" when the value was the old boolean
}

// DefaultSupportPaths are the files a change updates beside the code an issue
// names (docs with the generated option reference, frozen records, golden files, smoke sections),
// so scope drift ignores them unless ship.review.supportPaths replaces the list.
var DefaultSupportPaths = []string{
	"docs/**",
	"cmd/rota/testdata/frozen/**",
	"internal/*/testdata/golden/**",
	"test/sections/**",
}

// ParseReviewPolicy reads a ship.review value: the legacy booleans (true is
// full, false is none), a depth string, or the policy object. nil is the
// default, full.
func ParseReviewPolicy(v any) (ReviewPolicy, error) {
	bad := func(format string, a ...any) (ReviewPolicy, error) {
		return ReviewPolicy{}, fmt.Errorf("%s: %s", ReviewKey, fmt.Sprintf(format, a...))
	}
	switch x := v.(type) {
	case nil:
		return ReviewPolicy{Default: DepthFull}, nil
	case bool:
		if x {
			return ReviewPolicy{Default: DepthFull, legacy: "true"}, nil
		}
		return ReviewPolicy{Default: DepthNone, legacy: "false"}, nil
	case string:
		if d := ReviewDepth(x); d.valid() {
			return ReviewPolicy{Default: d}, nil
		}
		return bad("%q is not full, light or none", x)
	case *jsonx.Object:
		p := ReviewPolicy{Default: DepthFull}
		for _, key := range x.Keys() {
			val, _ := x.Get(key)
			switch key {
			case "default":
				s, _ := val.(string)
				if !ReviewDepth(s).valid() {
					return bad(`"default" must be full, light or none`)
				}
				p.Default = ReviewDepth(s)
			case "lightBelow":
				n, ok := val.(json.Number)
				i, err := strconv.Atoi(string(n))
				if !ok || err != nil || i < 0 {
					return bad(`"lightBelow" must be a whole number of changed lines, 0 or more`)
				}
				p.LightBelow = i
			case "labels":
				lo, ok := val.(*jsonx.Object)
				if !ok {
					return bad(`"labels" must be an object of label: depth`)
				}
				p.Labels = map[string]ReviewDepth{}
				for _, label := range lo.Keys() {
					dv, _ := lo.Get(label)
					s, _ := dv.(string)
					if label == "" || !ReviewDepth(s).valid() {
						return bad(`"labels" entry %q must map a label to full, light or none`, label)
					}
					p.Labels[label] = ReviewDepth(s)
				}
			case "paths":
				po, ok := val.(*jsonx.Object)
				if !ok {
					return bad(`"paths" must be an object of glob: depth`)
				}
				p.Paths = map[string]ReviewDepth{}
				for _, glob := range po.Keys() {
					dv, _ := po.Get(glob)
					s, _ := dv.(string)
					if strings.TrimSpace(glob) == "" || !ReviewDepth(s).valid() {
						return bad(`"paths" entry %q must map a glob to full, light or none`, glob)
					}
					// path.Match has no "**": only a trailing /** (a directory) is
					// supported, and any other use would silently match less.
					if _, err := path.Match(dirGlob(glob), ""); err != nil || strings.Contains(dirGlob(glob), "**") {
						return bad(`"paths" glob %q is not supported: use * within a segment, or a trailing /** for a directory`, glob)
					}
					p.Paths[glob] = ReviewDepth(s)
				}
			case "scopeDrift":
				b, ok := val.(bool)
				if !ok {
					return bad(`"scopeDrift" must be true or false`)
				}
				p.NoScopeDrift = !b
			case "supportPaths":
				arr, ok := val.([]any)
				if !ok {
					return bad(`"supportPaths" must be an array of globs`)
				}
				p.SupportPaths = []string{}
				for _, e := range arr {
					glob, _ := e.(string)
					if strings.TrimSpace(glob) == "" {
						return bad(`"supportPaths" entries must be non-empty glob strings`)
					}
					if _, err := path.Match(dirGlob(glob), ""); err != nil || strings.Contains(dirGlob(glob), "**") {
						return bad(`"supportPaths" glob %q is not supported: use * within a segment, or a trailing /** for a directory`, glob)
					}
					p.SupportPaths = append(p.SupportPaths, glob)
				}
			default:
				return bad("unknown field %q (want default, lightBelow, labels, paths, scopeDrift, supportPaths)", key)
			}
		}
		return p, nil
	}
	return bad("want true, false, full, light, none or a policy object")
}

// ReviewPolicyOf reads the policy from the merged config.
func ReviewPolicyOf(cfg any) (ReviewPolicy, error) {
	v, _ := Value(cfg, ReviewKey)
	return ParseReviewPolicy(v)
}

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

// ReviewInput is everything a depth is resolved from. Changed is the diff's
// changed-line count (negative: unknown). Files are the changed paths (nil:
// unknown). PRBody is the PR description ("" when there is no PR yet).
// IssueBody is the body of the branch's issue ("" when there is none). Tier
// is the change tier of the diff ("" when tiers do not apply).
type ReviewInput struct {
	Changed   int
	Labels    []string
	Files     []string
	PRBody    string
	IssueBody string
	Tier      change.Tier
	// Risky: a change.riskyPaths glob matched a changed file.
	Risky bool
}

// ReviewDecision is a resolved depth, the signal that decided it and why.
type ReviewDecision struct {
	Depth  ReviewDepth
	Signal string
	Why    string
}

// MayBeBelowFull reports whether the base depth can come out below full, the
// only case a Door line or scope drift can lift it. A caller skips reading the
// PR and the issue for them otherwise.
func (p ReviewPolicy) MayBeBelowFull() bool {
	if p.Default != DepthFull || p.LightBelow > 0 {
		return true
	}
	for _, d := range p.Labels {
		if d != DepthFull {
			return true
		}
	}
	return false
}

// NeedsIssue reports whether the branch's issue can change the depth: it holds
// a label to match, or a declared file set to drift from.
func (p ReviewPolicy) NeedsIssue() bool {
	return len(p.Labels) > 0 || (!p.NoScopeDrift && p.MayBeBelowFull())
}

var doorOneWay = regexp.MustCompile(`(?mi)^[ \t]*Door:[ \t]*one-way\b`)

// Resolve is Decide on a diff size and labels alone.
func (p ReviewPolicy) Resolve(changed int, labels []string) (ReviewDepth, string) {
	d := p.Decide(ReviewInput{Changed: changed, Labels: labels})
	return d.Depth, d.Why
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
func (p ReviewPolicy) Decide(in ReviewInput) ReviewDecision {
	base := p.base(in.Changed, in.Labels)
	if base.Signal == SignalLabel && base.Depth == DepthNone {
		return base
	}
	labelled := base.Signal == SignalLabel
	driftCap := DepthFull
	switch {
	case in.Tier == change.TierDocs && !labelled:
		base = ReviewDecision{DepthNone, SignalTier, "docs-only change (tier docs)"}
	case in.Tier == change.TierSmall:
		driftCap = DepthLight
		if !labelled && base.Depth.rank() > DepthLight.rank() {
			base = ReviewDecision{DepthLight, SignalTier, "small change (tier small)"}
		}
	}
	best := base
	raise := func(d ReviewDepth, signal, why string) {
		if d.rank() > best.Depth.rank() {
			best = ReviewDecision{d, signal, why}
		}
	}
	if DoorOneWay(in.PRBody) {
		raise(DepthFull, SignalDoor, "PR body says Door: one-way")
	}
	if in.Risky {
		raise(DepthFull, SignalRisky, "diff touches change.riskyPaths")
	}
	if !p.NoScopeDrift && in.Tier != change.TierDocs {
		if drift := scopeDrift(in.IssueBody, in.Files, p.supportPaths()); len(drift) > 0 {
			d := DepthFull
			if driftCap.rank() < d.rank() {
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
			if overlap.MatchPath(dirGlob(g), f) {
				raise(p.Paths[g], SignalPath, fmt.Sprintf("%s matches paths %s (%s)", f, g, p.Paths[g]))
				break
			}
		}
	}
	return best
}

// dirGlob turns a trailing /** into the directory prefix MatchPath understands.
func dirGlob(g string) string { return strings.TrimSuffix(g, "/**") }

// base is the depth from labels, then the size rule, then the default.
func (p ReviewPolicy) base(changed int, labels []string) ReviewDecision {
	var hit []string
	for _, l := range labels {
		if _, ok := p.Labels[l]; ok {
			hit = append(hit, l)
		}
	}
	if len(hit) > 0 {
		slices.Sort(hit)
		best := hit[0]
		for _, l := range hit[1:] {
			if p.Labels[l].rank() > p.Labels[best].rank() {
				best = l
			}
		}
		return ReviewDecision{p.Labels[best], SignalLabel, fmt.Sprintf("label %s", best)}
	}
	if p.Default == DepthFull && p.LightBelow > 0 && changed >= 0 && changed < p.LightBelow {
		return ReviewDecision{DepthLight, SignalSize, fmt.Sprintf("%d changed lines, under lightBelow %d", changed, p.LightBelow)}
	}
	if p.legacy != "" {
		return ReviewDecision{p.Default, SignalDefault, fmt.Sprintf("ship.review is %s", p.legacy)}
	}
	return ReviewDecision{p.Default, SignalDefault, "default depth"}
}

// pointersHeadRe matches a "Pointers" heading: the files an issue points at.
var pointersHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+pointers[ \t]*$`)

// supportPaths is the globs scope drift ignores.
func (p ReviewPolicy) supportPaths() []string {
	if p.SupportPaths == nil {
		return DefaultSupportPaths
	}
	return p.SupportPaths
}

// matchSupport reports whether file sits at or under a path the glob matches,
// so internal/*/testdata/golden/** covers internal/x/testdata/golden/a/b.txt.
func matchSupport(glob, file string) bool {
	glob = dirGlob(glob)
	segs := strings.Split(file, "/")
	for i := 1; i <= len(segs); i++ {
		if ok, err := path.Match(glob, strings.Join(segs[:i], "/")); err == nil && ok {
			return true
		}
	}
	return false
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
		a = dirGlob(a)
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
	for _, sec := range sections(issueBody, itembody.FilesHeadRe) {
		allowed = append(allowed, bulletPaths(sec, false)...)
	}
	for _, sec := range sections(issueBody, itembody.TouchesHeadRe) {
		allowed = append(allowed, bulletPaths(sec, true)...)
	}
	for _, sec := range sections(issueBody, pointersHeadRe) {
		allowed = append(allowed, bulletPaths(sec, true)...)
	}
	if len(allowed) == 0 {
		return nil
	}
	var out []string
	for _, f := range files {
		if slices.ContainsFunc(allowed, func(a string) bool { return overlap.MatchPath(dirGlob(a), f) }) ||
			slices.ContainsFunc(support, func(g string) bool { return matchSupport(g, f) }) ||
			sitsBesideDeclared(f, allowed) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// sections is every section under head, in order. A bundle's issue bodies are
// joined, so the joined text can hold one Files section per issue.
func sections(text string, head *regexp.Regexp) []string {
	var out []string
	for {
		sec, ok := itembody.Section(text, head)
		if !ok {
			return out
		}
		out = append(out, sec)
		text = text[head.FindStringIndex(text)[1]+len(sec):]
	}
}

// bulletPaths reads the paths of each bullet in a section: every backticked
// token and every bare word. pathLike keeps only entries with a slash or glob;
// otherwise (a Files section) a bullet with no backticks gives its first word.
func bulletPaths(section string, pathLike bool) []string {
	var out []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		line = strings.TrimSpace(line[2:])
		line = strings.TrimPrefix(strings.TrimPrefix(line, "[ ] "), "[x] ")
		// backticked tokens, plus the bare words outside the backticks
		var toks []string
		bare := line
		for rest := line; ; {
			a := strings.Index(rest, "`")
			if a < 0 {
				break
			}
			b := strings.Index(rest[a+1:], "`")
			if b < 0 {
				break
			}
			tok := rest[a+1 : a+1+b]
			toks = append(toks, tok)
			bare = strings.Replace(bare, "`"+tok+"`", " ", 1)
			rest = rest[a+b+2:]
		}
		words := strings.Fields(bare)
		if !pathLike && len(toks) == 0 && len(words) > 0 {
			words = words[:1] // a Files bullet with no backticks names its path first
		} else if !pathLike {
			words = nil
		}
		for _, w := range words {
			toks = append(toks, strings.Trim(w, ",;()"))
		}
		for _, tok := range toks {
			tok = strings.TrimSpace(tok)
			if tok == "" || (pathLike && !strings.ContainsAny(tok, "/*")) {
				continue
			}
			out = append(out, tok)
		}
	}
	return out
}
