package round

import (
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/harness"
)

// Blocked reasons of a bad harness: or model: label (exit 4, `blockedBy`).
const (
	BlockHarnessLabel = "harness label"
	BlockModelLabel   = "model label"
)

// Pick is the harness and model an item asks for; "" is no request.
type Pick struct{ Harness, Model string }

// modelID is what a model id may look like. The id lands in a shell launch
// line, and a label is typed by anyone who can triage the issue, so nothing
// else (spaces, quotes, `;`, `$`, backticks, a leading `-`) gets through.
var modelID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,99}$`)

// ValidModel reports whether v is a usable model id.
func ValidModel(v string) bool { return modelID.MatchString(v) }

// fileField reads a trailing `Harness:`/`Model:` field off a file-backend
// bullet; the value is one token.
var fileField = map[string]*regexp.Regexp{
	"Harness": regexp.MustCompile(`\bHarness:\s*(\S+)`),
	"Model":   regexp.MustCompile(`\bModel:\s*(\S+)`),
}

// PickOf reads what an item asks for: the harness:<kind> and model:<id>
// labels of an issue, the Harness:/Model: fields of a file bullet. A harness
// value rota does not know, or two of either key on one issue, is a refusal
// naming the label.
func PickOf(it backlog.Item) (Pick, error) {
	var p Pick
	if it.Number == 0 {
		if m := fileField["Harness"].FindStringSubmatch(it.Line); m != nil {
			if !harness.Valid(m[1]) {
				return p, blocked(BlockHarnessLabel, "%s has Harness: %s: harness must be one of %s", it.ID, m[1], strings.Join(harness.Kinds, ", "))
			}
			p.Harness = m[1]
		}
		if m := fileField["Model"].FindStringSubmatch(it.Line); m != nil {
			if !ValidModel(m[1]) {
				return p, blocked(BlockModelLabel, "%s has Model: %s: not a valid model id", it.ID, m[1])
			}
			p.Model = m[1]
		}
		return p, nil
	}
	hs, ms := labelsWith(it.Labels, "harness:"), labelsWith(it.Labels, "model:")
	if len(hs) > 1 {
		return p, blocked(BlockHarnessLabel, "%s has more than one harness: label (%s): keep one", it.ID, strings.Join(hs, ", "))
	}
	if len(ms) > 1 {
		return p, blocked(BlockModelLabel, "%s has more than one model: label (%s): keep one", it.ID, strings.Join(ms, ", "))
	}
	if len(hs) == 1 {
		v := strings.TrimPrefix(hs[0], "harness:")
		if !harness.Valid(v) {
			return p, blocked(BlockHarnessLabel, "%s has label %s: harness must be one of %s", it.ID, hs[0], strings.Join(harness.Kinds, ", "))
		}
		p.Harness = v
	}
	if len(ms) == 1 {
		v := strings.TrimPrefix(ms[0], "model:")
		if !ValidModel(v) {
			return p, blocked(BlockModelLabel, "%s has label %s: not a valid model id (letters, digits, . _ : / @ -)", it.ID, ms[0])
		}
		p.Model = v
	}
	return p, nil
}

func labelsWith(labels []string, prefix string) []string {
	var out []string
	for _, l := range labels {
		if strings.HasPrefix(l, prefix) {
			out = append(out, l)
		}
	}
	return out
}

// String lists the pick as labels, for messages.
func (p Pick) String() string {
	var out []string
	if p.Harness != "" {
		out = append(out, "harness:"+p.Harness)
	}
	if p.Model != "" {
		out = append(out, "model:"+p.Model)
	}
	return strings.Join(out, " ")
}

// resolveKind is the harness: --kind, else the item's label, else the slot's
// recorded kind, else claude.
func resolveKind(flag, label, slot string) string {
	for _, k := range []string{flag, label, slot} {
		if k != "" {
			return k
		}
	}
	return harness.Default
}

// resolveModel is the model: --model, else the item's label, else the tier
// map's entry; "" leaves the harness its own default.
func resolveModel(flag, label, tier string) string {
	for _, m := range []string{flag, label, tier} {
		if m != "" {
			return m
		}
	}
	return ""
}
