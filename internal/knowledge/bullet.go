package knowledge

import (
	"fmt"
	"regexp"
	"strings"
)

// The local dated bullet is "- **Title** — body <!-- YYYY-MM-DD -->", with an
// optional commit sha beside the date ("<!-- YYYY-MM-DD sha:abc1234 -->").
// The stamp is always the last thing on the line: Add renders it, Amend keeps
// it final, and Query recognizes a line only by it. This file owns that
// shape; the external format (extknowledge) has its own stamp fields.

// titleRe reads the title of any titled bullet, dated or not.
var titleRe = regexp.MustCompile(`^- \*\*([^*]+)\*\*`)

// bulletRe splits a dated bullet into title, body and trailing stamp.
var bulletRe = regexp.MustCompile(`^- \*\*([^*]+)\*\* — (.*?)(\s*<!-- \d{4}-\d{2}-\d{2}( sha:[0-9a-f]{7,40})?\s*-->\s*)$`)

// renderBullet is the line Add writes for a new entry.
func renderBullet(title, body, date, sha string) string {
	stamp := date
	if sha != "" {
		stamp += " sha:" + sha
	}
	return fmt.Sprintf("- **%s** — %s <!-- %s -->", title, body, stamp)
}

// parseBullet splits a bullet line (right-trimmed) into its title, body and
// stamp; ok is false when the line is not a dated bullet.
func parseBullet(line string) (title, body, stamp string, ok bool) {
	m := bulletRe.FindStringSubmatch(line)
	if m == nil {
		return "", "", "", false
	}
	return strings.TrimSpace(m[1]), m[2], m[3], true
}

// appendToBullet adds text to the end of a bullet's body, ahead of its stamp.
// A line without a final stamp (undated, or a stamp followed by stray text)
// gets the text at the end, as before.
func appendToBullet(line, text string) string {
	trimmed := strings.TrimRight(line, " \t\r\f\v")
	m := bulletRe.FindStringSubmatchIndex(trimmed)
	if m == nil {
		return line + " " + text
	}
	stampStart := m[6] // start of group 3, the stamp with its leading space
	return strings.TrimRight(trimmed[:stampStart], " \t") + " " + text + trimmed[stampStart:]
}
