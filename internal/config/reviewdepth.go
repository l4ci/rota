package config

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/itembody"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/overlap"
)

// ReviewDepth is how much review a branch gets before it ships: full runs
// /rota-review as is, light runs only its Standards reviewer, none skips it.
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

// ReviewPolicy is ship.review in object form. Default applies when no label
// and no size rule does. LightBelow, when above 0, downgrades a Default of full
// to light for a diff of fewer changed lines. Labels override both. Paths maps
// a glob to the least depth a changed path matching it gets. NoScopeDrift turns
// off the rule that a diff outside the issue's declared files resolves full.
type ReviewPolicy struct {
	Default      ReviewDepth
	LightBelow   int
	Labels       map[string]ReviewDepth
	Paths        map[string]ReviewDepth
	NoScopeDrift bool
	legacy       string // "true" or "false" when the value was the old boolean
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
					p.Paths[glob] = ReviewDepth(s)
				}
			case "scopeDrift":
				b, ok := val.(bool)
				if !ok {
					return bad(`"scopeDrift" must be true or false`)
				}
				p.NoScopeDrift = !b
			default:
				return bad("unknown field %q (want default, lightBelow, labels, paths, scopeDrift)", key)
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
)

// ReviewInput is everything a depth is resolved from. Changed is the diff's
// changed-line count (negative: unknown). Files are the changed paths (nil:
// unknown). PRBody is the PR description ("" when there is no PR yet).
// IssueBody is the body of the branch's issue ("" when there is none).
type ReviewInput struct {
	Changed   int
	Labels    []string
	Files     []string
	PRBody    string
	IssueBody string
}

// ReviewDecision is a resolved depth, the signal that decided it and why.
type ReviewDecision struct {
	Depth  ReviewDepth
	Signal string
	Why    string
}

// MayBeLight reports whether the base depth can come out light, the only
// case a Door line or scope drift can lift it. A caller skips reading the PR and
// the issue for them otherwise.
func (p ReviewPolicy) MayBeLight() bool {
	if p.Default == DepthLight || (p.Default == DepthFull && p.LightBelow > 0) {
		return true
	}
	for _, d := range p.Labels {
		if d == DepthLight {
			return true
		}
	}
	return false
}

// NeedsIssue reports whether the branch's issue can change the depth: it holds
// a label to match, or a declared file set to drift from.
func (p ReviewPolicy) NeedsIssue() bool {
	return len(p.Labels) > 0 || (!p.NoScopeDrift && p.MayBeLight())
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
// glob. Door and scope drift only lift a light review: a default of none is the
// review switched off, which they do not override.
func (p ReviewPolicy) Decide(in ReviewInput) ReviewDecision {
	base := p.base(in.Changed, in.Labels)
	if base.Signal == SignalLabel && base.Depth == DepthNone {
		return base
	}
	best := base
	raise := func(d ReviewDepth, signal, why string) {
		if d.rank() > best.Depth.rank() {
			best = ReviewDecision{d, signal, why}
		}
	}
	if base.Depth == DepthLight {
		if doorOneWay.MatchString(in.PRBody) {
			raise(DepthFull, SignalDoor, "PR body says Door: one-way")
		}
		if !p.NoScopeDrift {
			if drift := scopeDrift(in.IssueBody, in.Files); len(drift) > 0 {
				raise(DepthFull, SignalScopeDrift, "diff touches files the issue never named: "+strings.Join(drift, ", "))
			}
		}
	}
	globs := make([]string, 0, len(p.Paths))
	for g := range p.Paths {
		globs = append(globs, g)
	}
	slices.Sort(globs)
	for _, g := range globs {
		for _, f := range in.Files {
			if overlap.MatchPath(g, f) {
				raise(p.Paths[g], SignalPath, fmt.Sprintf("%s matches paths %s (%s)", f, g, p.Paths[g]))
				break
			}
		}
	}
	return best
}

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

// scopeDrift lists the changed files the issue body never named. The declared
// set is its "## Files" entries plus the path-like (slash or glob) entries of
// "## Touches". An issue that declares none has no set to drift from.
func scopeDrift(issueBody string, files []string) []string {
	var allowed []string
	if sec, ok := itembody.Section(issueBody, itembody.FilesHeadRe); ok {
		allowed = append(allowed, bulletPaths(sec, false)...)
	}
	if sec, ok := itembody.Section(issueBody, itembody.TouchesHeadRe); ok {
		allowed = append(allowed, bulletPaths(sec, true)...)
	}
	if len(allowed) == 0 {
		return nil
	}
	var out []string
	for _, f := range files {
		if !slices.ContainsFunc(allowed, func(a string) bool { return overlap.MatchPath(a, f) }) {
			out = append(out, f)
		}
	}
	return out
}

// bulletPaths reads the path of each bullet in a section: the first backticked
// token, else the first word. pathLike keeps only entries with a slash or glob.
func bulletPaths(section string, pathLike bool) []string {
	var out []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		line = strings.TrimSpace(line[2:])
		line = strings.TrimPrefix(strings.TrimPrefix(line, "[ ] "), "[x] ")
		tok := ""
		if a := strings.Index(line, "`"); a >= 0 {
			if b := strings.Index(line[a+1:], "`"); b >= 0 {
				tok = line[a+1 : a+1+b]
			}
		}
		if tok == "" {
			if f := strings.Fields(line); len(f) > 0 {
				tok = f[0]
			}
		}
		tok = strings.TrimSpace(tok)
		if tok == "" || (pathLike && !strings.ContainsAny(tok, "/*")) {
			continue
		}
		out = append(out, tok)
	}
	return out
}
