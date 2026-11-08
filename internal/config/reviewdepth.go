package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pathglob"
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
					if !pathglob.ValidEntry(glob) {
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
					if !pathglob.ValidEntry(glob) {
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

// Legacy is "true" or "false" when the policy came from the old boolean, else "".
func (p ReviewPolicy) Legacy() string { return p.legacy }

// EffectiveSupportPaths is the globs scope drift ignores: SupportPaths, or
// DefaultSupportPaths when none are set.
func (p ReviewPolicy) EffectiveSupportPaths() []string {
	if p.SupportPaths == nil {
		return DefaultSupportPaths
	}
	return p.SupportPaths
}
