package labels

import (
	"sort"
	"strings"
)

const (
	Missing        = "missing"
	Metadata       = "metadata"
	DefaultOverlap = "default-overlap"
	UnusedClaim    = "unused-claim"
	Unknown        = "unknown"
)

// Change is a sync finding. Only Missing and Metadata are writable; Current
// is nil for a missing definition. Report-only findings always carry the
// existing metadata and are never repaired, even when recognized by a prefix.
type Change struct {
	Kind    string
	Label   Label
	Current *Label
}

// Diff compares definitions, never issues. used is the complete set of names
// on open issues. An unused claim is reported before considering its metadata.
func Diff(entries []Entry, have []Label, used map[string]bool, metadata bool) []Change {
	var out []Change
	byName := map[string]Label{}
	for _, l := range have {
		byName[l.Name] = l
	}
	for _, e := range entries {
		if e.Prefix {
			continue
		}
		if _, ok := byName[e.Name]; !ok {
			out = append(out, Change{Kind: Missing, Label: e.Label})
		}
	}
	for _, current := range have {
		cur := current
		if strings.HasPrefix(cur.Name, ClaimPrefix) && !used[cur.Name] {
			out = append(out, Change{UnusedClaim, cur, &cur})
			continue
		}
		if want, ok := Find(entries, cur.Name); ok {
			if metadata && (cur.Description != want.Description || normalizeColor(cur.Color) != normalizeColor(want.Color)) {
				out = append(out, Change{Metadata, want, &cur})
			}
			continue
		}
		kind := Unknown
		switch cur.Name {
		case "bug", "enhancement", "documentation":
			kind = DefaultOverlap
		}
		out = append(out, Change{kind, cur, &cur})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].Label.Name < out[j].Label.Name
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func normalizeColor(c string) string { return strings.ToLower(strings.TrimPrefix(c, "#")) }
