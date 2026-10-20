package milestone

import (
	"regexp"
	"strings"
)

// KindDelivery and KindDecision are the milestone kinds: a delivery milestone
// ships code, a decision milestone maps an effort too foggy to slice into
// research and grilling items and logs what each one settled.
const (
	KindDelivery = "delivery"
	KindDecision = "decision"
)

// Kinds are the legal kinds.
var Kinds = []string{KindDelivery, KindDecision}

var fmKind = regexp.MustCompile(`(?m)^kind:[ \t]*(\S+)`)

// KindOf is the kind in the plan's frontmatter; a plan with none is delivery.
func KindOf(text string) string {
	if m := fmBlock.FindStringSubmatch(text); m != nil {
		if k := fmKind.FindStringSubmatch(m[1]); k != nil {
			return k[1]
		}
	}
	return KindDelivery
}

// WithKind turns a freshly stubbed plan into one of the given kind. A decision
// plan gets `kind: decision` in its frontmatter and the two sections the
// closes write into and the fog lives in. Delivery returns text unchanged.
func WithKind(text, kind string) string {
	if kind != KindDecision {
		return text
	}
	if m := fmBlock.FindStringSubmatchIndex(text); m != nil {
		text = text[:m[3]] + "\nkind: " + KindDecision + text[m[3]:]
	}
	return strings.TrimRight(text, "\n") + "\n\n## " + DecisionsSection + "\n\n_(none yet: each closed decision item adds one line)_\n\n" +
		"## Not yet specified\n\n_(the fog: what no decision covers yet)_\n"
}
