package knowledge

import (
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/bulletfmt"
)

// The local dated bullet is "- **Title** — body <!-- YYYY-MM-DD -->", with an
// optional commit sha beside the date ("<!-- YYYY-MM-DD sha:abc1234 -->").
// The stamp is always the last thing on the line: Add renders it, Amend keeps
// it final, and Query recognizes a line only by it. The title and line
// render are shared with the external format (internal/bulletfmt); this file owns
// the stamp, which extknowledge writes with its own fields.

// stampRe is the stamp a local bullet ends with: a date and an optional sha.
var stampRe = regexp.MustCompile(`^\s*<!-- ` + bulletfmt.DatePattern + `( sha:[0-9a-f]{7,40})?\s*-->\s*$`)

// renderBullet is the line Add writes for a new entry.
func renderBullet(title, body, date, sha string) string {
	stamp := date
	if sha != "" {
		stamp += " sha:" + sha
	}
	return bulletfmt.Render(title, body, stamp)
}

// parseBullet splits a bullet line (right-trimmed) into its title, body and
// stamp through bulletfmt.Parse; ok is false when the line is not a dated
// bullet: no title, no " — " after it, or a stamp that is not date and sha.
func parseBullet(line string) (title, body, stamp string, ok bool) {
	p := bulletfmt.Parse(line)
	if !p.Titled || !p.Dashed || !stampRe.MatchString(p.Stamp) {
		return "", "", "", false
	}
	return p.Title, p.Body, p.Stamp, true
}

// appendToBullet adds text to the end of a bullet's body, ahead of its stamp.
// A line without a final stamp (undated, or a stamp followed by stray text)
// gets the text at the end, as before.
func appendToBullet(line, text string) string {
	trimmed := strings.TrimRight(line, " \t\r\f\v")
	if _, _, _, ok := parseBullet(trimmed); !ok {
		return line + " " + text
	}
	body, stamp := bulletfmt.SplitStamp(trimmed)
	return strings.TrimRight(body, " \t") + " " + text + stamp
}
