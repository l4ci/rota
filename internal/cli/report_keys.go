package cli

import (
	"regexp"

	"github.com/l4ci/rota/internal/tracker"
)

// Keep this token grammar aligned with tracker.ClosesByKey: report recipients
// follow the same branch/title convention as the tracker that closes them.
var reportKeyToken = regexp.MustCompile(`(?:^|[^A-Za-z0-9])([A-Za-z][A-Za-z0-9_]*-[0-9]+)`)

// reportKeyItems returns issue keys named by the key tracker's closing convention.
func reportKeyItems(branch, title string) []string {
	var out []string
	seen := map[tracker.IssueKey]bool{}
	for _, text := range []string{branch, title} {
		for _, token := range reportKeyToken.FindAllStringSubmatch(text, -1) {
			key, ok := tracker.ParseKey(token[1])
			if !ok || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, key.String())
		}
	}
	return out
}
