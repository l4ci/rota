package backlog

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	// closeRefRe is a closing keyword followed by a comma/"and" list of #N.
	closeRefRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b:?[ \t]+#\d+(?:[ \t]*(?:,|,?[ \t]+and)[ \t]*#\d+)*`)
	hashNumRe  = regexp.MustCompile(`#(\d+)`)
	branchNum  = regexp.MustCompile(`^[^/]+/(\d+)(?:-|$)`)
)

// FindIssueRefs lists the issue refs ("#12") that commit-message text closes
// (Closes/Fixes/Resolves and their -s/-d/-ed variants, any case, comma lists),
// deduplicated, in order of first appearance.
func FindIssueRefs(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range closeRefRe.FindAllString(text, -1) {
		for _, n := range hashNumRe.FindAllStringSubmatch(m, -1) {
			num, err := strconv.Atoi(n[1])
			if err != nil || num <= 0 {
				continue
			}
			id := "#" + strconv.Itoa(num)
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// BranchIssueRef is the issue ref ("#69") in a branch named
// "<agent>/<N>-slug", or "" when the name has none.
func BranchIssueRef(branch string) string {
	m := branchNum.FindStringSubmatch(strings.TrimSpace(branch))
	if m == nil {
		return ""
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return ""
	}
	return "#" + strconv.Itoa(n)
}
