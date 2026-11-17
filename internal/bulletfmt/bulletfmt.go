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
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/stamp"
)

// ErrInvalid is the class of every refusal of text a bullet cannot carry.
var ErrInvalid = errors.New("invalid bullet text")

// titleRe reads the title of any titled bullet, dated or not.
var titleRe = regexp.MustCompile(`^- \*\*([^*]+)\*\*`)

// CheckTitle refuses a title no reader could find again: a line break, a
// stamp closer or a "*", which titleRe cannot parse. Both stores' Add call it.
func CheckTitle(title string) error {
	if strings.ContainsAny(title, "\r\n") {
		return fmt.Errorf("%w: title must be a single line", ErrInvalid)
	}
	if strings.Contains(title, "-->") {
		return fmt.Errorf("%w: title must not contain %q", ErrInvalid, "-->")
	}
	if strings.Contains(title, "*") {
		return fmt.Errorf("%w: title must not contain %q", ErrInvalid, "*")
	}
	return nil
}

// CheckDate refuses a stamp date that is not a real YYYY-MM-DD; "" is allowed
// and means "today" to the caller.
func CheckDate(date string) error {
	if date == "" {
		return nil
	}
	if _, err := stamp.ParseDate(date); err != nil {
		return fmt.Errorf("%w: date must be a real YYYY-MM-DD, got %q", ErrInvalid, date)
	}
	return nil
}

// Title returns the bold title of a bullet line (surrounding whitespace, the
// title's own included, is ignored). ok is false when the line is not a titled bullet.
func Title(line string) (title string, ok bool) {
	m := titleRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
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

// SelectTitle is the one title-selection rule both knowledge stores apply:
// it returns the indexes of titles equal to want, ignoring case and
// surrounding whitespace. An empty entry (a bullet with no bold title) never
// matches. More than one index means the title is ambiguous, and the caller
// refuses and names every hit.
func SelectTitle(titles []string, want string) []int {
	want = strings.ToLower(strings.TrimSpace(want))
	var hits []int
	for i, t := range titles {
		if t != "" && strings.ToLower(t) == want {
			hits = append(hits, i)
		}
	}
	return hits
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

// stampTail is the final "<!-- … -->" comment of a bullet's first line.
var stampTail = regexp.MustCompile(`\s*<!--[^>]*-->\s*$`)

// SplitStamp splits a bullet line into its text and its final "<!-- … -->"
// stamp (leading whitespace included); stamp is "" when the line has none.
// Only the last comment on the line is the stamp.
func SplitStamp(line string) (text, stamp string) {
	stamp = stampTail.FindString(line)
	return strings.TrimSuffix(line, stamp), stamp
}

// DatePattern is a stamp date, YYYY-MM-DD, as a regexp fragment for a store
// that matches its own stamp shape; dateRe tests one token.
const DatePattern = `\d{4}-\d{2}-\d{2}`

var dateRe = regexp.MustCompile(`^` + DatePattern + `$`)

// IsDate reports whether a stamp token is a YYYY-MM-DD date.
func IsDate(tok string) bool { return dateRe.MatchString(tok) }

// StampFields is the space-separated tokens inside a stamp's "<!-- … -->"; ""
// gives none. Every reader and rewriter of stamp tokens starts here.
func StampFields(stamp string) []string {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(stamp), "<!--"), "-->"))
	return strings.Fields(inner)
}

// Parts is a bullet line split into its title, body and stamp.
type Parts struct {
	Title  string // the trimmed **title**; "" when the line has none
	Body   string // text between the title and the stamp, trimmed, without the dash
	Stamp  string // the final "<!-- … -->", leading whitespace included; "" when none
	Titled bool   // the line starts "- **title**"
	Dashed bool   // the title is followed by " — " exactly
}

// Parse is the one splitter of a bullet line: both stores read title, body and
// stamp through it, so a padded title or a loose dash parses alike. A line that
// is not a "- " bullet has no title and its trimmed text as the body. What the
// stamp may hold stays with each store.
func Parse(line string) Parts {
	text, stamp := SplitStamp(line)
	p := Parts{Stamp: stamp}
	if m := titleRe.FindStringSubmatch(text); m != nil {
		p.Titled, p.Title = true, strings.TrimSpace(m[1])
		text = text[len(m[0]):]
		// SplitStamp took the space before the stamp, so an empty body leaves a bare " —".
		p.Dashed = strings.HasPrefix(text, " — ") || (stamp != "" && text == " —")
		text = strings.TrimPrefix(strings.TrimSpace(text), "—")
	} else {
		text = strings.TrimPrefix(text, "- ")
	}
	p.Body = strings.TrimSpace(text)
	return p
}

// SelectFragment is the one fragment-selection rule both knowledge stores
// apply to Replace and Amend: it returns the indexes of the bullets whose text
// contains fragment, case-sensitively.
//
//   - A bullet starts at a column-0 "- " line. An indented "- " line is a
//     continuation of the bullet above it (see Continues), never a bullet.
//   - A bullet's text is its first line without the stamp, plus its
//     continuation lines, so wrapped bullets match as a whole. The stamp is
//     never searched.
//
// More than one index means the fragment is ambiguous, and the caller refuses.
func SelectFragment(lines []string, fragment string) []int {
	var hits []int
	for i, l := range lines {
		if !strings.HasPrefix(l, "- ") {
			continue
		}
		first, _ := SplitStamp(l)
		text := first
		for _, c := range lines[i+1 : End(lines, i)] {
			text += "\n" + c
		}
		if strings.Contains(text, fragment) {
			hits = append(hits, i)
		}
	}
	return hits
}

// ReplaceIn swaps every old for new in a bullet's lines (the span SelectFragment
// matched), leaving the first line's stamp untouched. It returns a new slice.
func ReplaceIn(block []string, old, new string) []string {
	out := make([]string, len(block))
	for i, l := range block {
		if i == 0 {
			text, stamp := SplitStamp(l)
			out[i] = strings.ReplaceAll(text, old, new) + stamp
			continue
		}
		out[i] = strings.ReplaceAll(l, old, new)
	}
	return out
}

// LineStart is the offset of lines[i] in strings.Join(lines, "\n"); an i past
// the last line gives the length of that text.
func LineStart(lines []string, i int) int {
	off := 0
	for k := 0; k < i && k < len(lines); k++ {
		off += len(lines[k]) + 1
	}
	if n := len(strings.Join(lines, "\n")); off > n {
		return n
	}
	return off
}

// DeleteSpan removes text[a:b], a bullet (a at its first byte, b just past its
// last newline). It is the one blank-line collapse on delete: when a blank
// line sits on both sides, the two would join into a double blank, so one of
// them goes too.
func DeleteSpan(text string, a, b int) string {
	if strings.HasSuffix(text[:a], "\n\n") && strings.HasPrefix(text[b:], "\n") {
		b++
	}
	return text[:a] + text[b:]
}

// DeleteBullet removes the bullet at lines[i] with its continuation lines,
// collapsing blanks as DeleteSpan does, and returns the remaining lines.
func DeleteBullet(lines []string, i int) []string {
	text := strings.Join(lines, "\n")
	return strings.Split(DeleteSpan(text, LineStart(lines, i), LineStart(lines, End(lines, i))), "\n")
}
