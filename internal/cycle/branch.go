// Package cycle holds the pure decisions behind the cycle bookkeeping verbs
// (`rota work start`, `rota ship finish`): names and classifications that a
// caller feeds with literals.
package cycle

import "strings"

// slugMax caps the title slug so branch names stay readable.
const slugMax = 40

// BranchName is the default branch for a cycle: rota/<id lowercased>-<slug of
// the title>. The ID leads the name so backlog.BranchIssueRef reads the issue
// number back from it.
func BranchName(id, title string) string {
	name := "rota/" + strings.ToLower(strings.TrimSpace(id))
	if s := slug(title); s != "" {
		name += "-" + s
	}
	return name
}

// slug lowercases s, collapses every run of non-alphanumerics to one "-",
// trims the ends and cuts at slugMax on a "-" boundary.
func slug(s string) string {
	var b strings.Builder
	dash := true // swallows leading separators
	for _, r := range strings.ToLower(s) {
		if r < 128 && (r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimRight(b.String(), "-")
	if len(out) <= slugMax {
		return out
	}
	cut := out[:slugMax+1]
	if i := strings.LastIndex(cut[:slugMax], "-"); i > 0 && cut[slugMax] != '-' {
		return cut[:i]
	}
	return strings.TrimRight(out[:slugMax], "-")
}
