package marker

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// VisibleMax caps what Visible returns, in runes, so a posted comment stays
// under the 32767-character limit of Jira (the tightest tracker).
const VisibleMax = 32000

// anyRe is one rota (or legacy hv) marker on a line.
var anyRe = regexp.MustCompile(`(?:` + regexp.QuoteMeta(Prefix) + `|` + regexp.QuoteMeta(LegacyPrefix) + `)([^\n]*?) -->`)

// Visible is what a rota comment shows people on a tracker that keeps the full
// body elsewhere: the body without its markers, blank runs and trailing space
// folded, or a short note naming the first marker when nothing else is left.
func Visible(body string) string {
	first := anyRe.FindStringSubmatch(body)
	text := anyRe.ReplaceAllString(body, "")
	var lines []string
	blank := false
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(l) == "" {
			if !blank && len(lines) > 0 {
				lines = append(lines, "")
			}
			blank = true
			continue
		}
		blank = false
		lines = append(lines, strings.TrimRightFunc(l, unicode.IsSpace))
	}
	text = strings.Join(lines, "\n")
	if text == "" && first != nil {
		text = "(rota " + first[1] + ")"
	}
	if utf8.RuneCountInString(text) > VisibleMax {
		text = string([]rune(text)[:VisibleMax]) + "\n\n…"
	}
	return text
}
