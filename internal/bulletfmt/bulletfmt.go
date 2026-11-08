// Package bulletfmt owns the knowledge bullet line shared by the local store
// (internal/knowledge) and the external one (internal/extknowledge):
//
//   - **Title** — body <!-- stamp -->
//     indented continuation lines
//
// It parses the title, renders the line, scans for a duplicate title and finds
// where a bullet ends. What goes inside the stamp (a commit sha, a source, the
// tools seen) and where the bullets live stay with each store.
package bulletfmt

import (
	"fmt"
	"regexp"
	"strings"
)

// titleRe reads the title of any titled bullet, dated or not.
var titleRe = regexp.MustCompile(`^- \*\*([^*]+)\*\*`)

// Title returns the bold title of a bullet line (surrounding whitespace is
// ignored). ok is false when the line is not a titled bullet.
func Title(line string) (title string, ok bool) {
	m := titleRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Render is the line a store writes for a new entry; stamp is the text between
// "<!-- " and " -->".
func Render(title, body, stamp string) string {
	return fmt.Sprintf("- **%s** — %s <!-- %s -->", title, body, stamp)
}

// HasTitle reports whether any of the lines is a bullet titled title, ignoring
// case.
func HasTitle(lines []string, title string) bool {
	lower := strings.ToLower(title)
	for _, l := range lines {
		if t, ok := Title(l); ok && strings.ToLower(t) == lower {
			return true
		}
	}
	return false
}

// Continues reports whether a line continues the bullet above it: it is
// indented. A blank line or unindented prose does not.
func Continues(line string) bool {
	return line != "" && (line[0] == ' ' || line[0] == '\t')
}

// End is the index just past the bullet starting at lines[i]: its own line and
// the indented lines that continue it.
func End(lines []string, i int) int {
	end := i + 1
	for end < len(lines) && Continues(lines[end]) {
		end++
	}
	return end
}
