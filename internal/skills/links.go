package skills

import (
	"path"
	"regexp"
	"strings"
)

var (
	linkRe   = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]*)(?:\s+"[^"]*")?\)`)
	codeSpan = regexp.MustCompile("`[^`\n]*`")
	scheme   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

// Links are the relative link targets of a markdown body, in order of first
// appearance and without duplicates. Code spans and fenced blocks hold no
// links, URLs, absolute paths and bare anchors are not relative files, and an
// anchor or query is cut off (x.md#sec is x.md).
func Links(body []byte) []string {
	var out []string
	seen := map[string]bool{}
	fence := ""
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			continue
		}
		line = codeSpan.ReplaceAllString(line, "")
		for _, m := range linkRe.FindAllStringSubmatch(line, -1) {
			target := m[1]
			if i := strings.IndexAny(target, "#?"); i >= 0 {
				target = target[:i]
			}
			if target == "" || strings.HasPrefix(target, "/") || scheme.MatchString(target) {
				continue
			}
			target = path.Clean(target)
			if !seen[target] {
				seen[target] = true
				out = append(out, target)
			}
		}
	}
	return out
}
