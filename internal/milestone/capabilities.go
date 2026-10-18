package milestone

import (
	"regexp"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/itembody"
)

// Capability is one contract a milestone changes: its normalised name and
// whether the milestone adds it (new), changes it (modified) or drops it (removed).
type Capability struct {
	Name, Kind string
}

var capTagRe = regexp.MustCompile(`(?i)\s*\(\s*(new|modified|removed)\s*\)\s*$`)

// Capabilities parses the `## Capabilities` section of a milestone body: one
// contract per bullet, optionally ending in (new), (modified) or (removed).
// An untagged bullet is modified. Names are normalised like a Touches entry,
// deduplicated (first wins) and sorted. It returns nil when there are none.
func Capabilities(body string) []Capability {
	sec, ok := itembody.Section(body, itembody.CapabilitiesHeadRe)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []Capability
	for _, line := range strings.Split(sec, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		rest, kind := line[2:], "modified"
		if m := capTagRe.FindStringSubmatchIndex(rest); m != nil {
			kind = strings.ToLower(rest[m[2]:m[3]])
			rest = rest[:m[0]]
		}
		name := itembody.Entry(rest)
		if name == "" || seen[name] || (strings.HasPrefix(name, "_(") && strings.HasSuffix(name, ")_")) {
			continue
		}
		seen[name] = true
		out = append(out, Capability{Name: name, Kind: kind})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CapabilityNames lists the names of caps in order.
func CapabilityNames(caps []Capability) []string {
	names := make([]string, 0, len(caps))
	for _, c := range caps {
		names = append(names, c.Name)
	}
	return names
}
