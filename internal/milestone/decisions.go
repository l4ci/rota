package milestone

import (
	"strings"

	"github.com/l4ci/rota/internal/section"
)

// DecisionsSection is the plan section a decision milestone keeps one line per
// closed decision item in.
const DecisionsSection = "Decisions"

// AppendDecision adds `- <ref>: <gist>` under the plan's "## Decisions" section,
// creating the section when it is missing and dropping its `_(...)_` placeholder.
// changed is false when a line for ref is already there, so a retried close
// writes nothing twice. The gist is one line: newlines collapse to spaces.
func AppendDecision(text, ref, gist string) (out string, changed bool) {
	line := "- " + ref + ": " + strings.Join(strings.Fields(gist), " ")
	var lines []string
	for _, l := range strings.Split(section.Body(text, DecisionsSection), "\n") {
		l = strings.TrimRight(l, " \t\r")
		switch {
		case l == "", strings.HasPrefix(l, "_("):
		case strings.HasPrefix(l, "- "+ref+":"):
			return text, false
		default:
			lines = append(lines, l)
		}
	}
	body := "\n" + strings.Join(append(lines, line), "\n") + "\n"
	if s, e, ok := section.Find(text, DecisionsSection); ok && e < len(text) && s <= e {
		body += "\n"
	}
	return section.Replace(text, DecisionsSection, body), true
}
