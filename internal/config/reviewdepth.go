package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/l4ci/rota/internal/jsonx"
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
// to light for a diff of fewer changed lines. Labels override both.
type ReviewPolicy struct {
	Default    ReviewDepth
	LightBelow int
	Labels     map[string]ReviewDepth
	legacy     string // "true" or "false" when the value was the old boolean
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
			default:
				return bad("unknown field %q (want default, lightBelow, labels)", key)
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

// Resolve picks the depth for a diff of changed lines (negative: unknown) on an
// issue or PR carrying labels, and says why. A label override beats the size
// rule; when several labels match, the strictest depth wins.
func (p ReviewPolicy) Resolve(changed int, labels []string) (ReviewDepth, string) {
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
		return p.Labels[best], fmt.Sprintf("label %s", best)
	}
	if p.Default == DepthFull && p.LightBelow > 0 && changed >= 0 && changed < p.LightBelow {
		return DepthLight, fmt.Sprintf("%d changed lines, under lightBelow %d", changed, p.LightBelow)
	}
	if p.legacy != "" {
		return p.Default, fmt.Sprintf("ship.review is %s", p.legacy)
	}
	return p.Default, "default depth"
}
