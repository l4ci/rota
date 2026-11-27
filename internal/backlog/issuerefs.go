package backlog

import (
	"regexp"
	"strconv"
)

var (
	// closeRefRe is a closing keyword followed by a comma/"and" list of #N.
	closeRefRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b:?[ \t]+#\d+(?:[ \t]*(?:,|,?[ \t]+and)[ \t]*#\d+)*`)
	hashNumRe  = regexp.MustCompile(`#(\d+)`)
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
