package milestone

import (
	"regexp"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/pystr"
)

// The milestone title grammar: "<id> — <title>". Title writes it (detail
// heading, overview entry, native tracker milestone) and ParseTitle reads it,
// so the format lives in this one file.

// titleSep is the em dash Title writes between the ID and the title.
const titleSep = " — "

var (
	titleIDRe   = regexp.MustCompile(`\A(` + TokenPattern + `)`)
	titleRestRe = regexp.MustCompile(`\A[` + pystr.SpaceClass + `]*[\x{2014}\x{2013}-][` + pystr.SpaceClass + `]*(.*)`)
)

// Title is the "<id> — <title>" text milestone headings and native milestones carry.
func Title(id, title string) string { return id + titleSep + title }

// ParseTitle splits "M02 — Sharing" into "M02" and "Sharing". The ID must not
// run into a word character; rest is "" when the title has no dash part.
// Em dash, en dash and hyphen all separate, so tracker-typed titles parse.
func ParseTitle(title string) (id, rest string, ok bool) {
	t := pystr.Strip(title)
	loc := titleIDRe.FindStringSubmatchIndex(t)
	if loc == nil {
		return "", "", false
	}
	end := loc[3]
	if end < len(t) {
		if r, _ := utf8.DecodeRuneInString(t[end:]); pystr.IsWord(r) {
			return "", "", false
		}
	}
	if m := titleRestRe.FindStringSubmatch(t[end:]); m != nil {
		rest = m[1]
	}
	return t[:end], rest, true
}
