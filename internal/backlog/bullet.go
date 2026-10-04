package backlog

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/section"
)

// FieldNames are the trailing "Name: value" fields a bullet can carry, in
// canonical order (_TODO_FIELD_NAMES).
var FieldNames = []string{"Detail", "Related", "Milestone", "Repos", "Subsystem", "Captured", "Since"}

// SettableFields are the lowercase names SetField accepts (_SETTABLE_FIELDS).
// Since and Captured are stamped by capture and must not be hand-set; the
// title is structural.
var SettableFields = []string{"milestone", "related", "repos", "subsystem", "detail"}

// Fields holds the trailing fields of one bullet; a missing field is "".
type Fields struct {
	Detail, Related, Milestone, Repos, Subsystem, Captured, Since string
}

// Get returns a field by its lowercase name ("detail", "since", ...), or ""
// for an unknown name.
func (f Fields) Get(name string) string {
	switch name {
	case "detail":
		return f.Detail
	case "related":
		return f.Related
	case "milestone":
		return f.Milestone
	case "repos":
		return f.Repos
	case "subsystem":
		return f.Subsystem
	case "captured":
		return f.Captured
	case "since":
		return f.Since
	}
	return ""
}

// set stores v under the lowercase field name.
func (f *Fields) set(name, v string) {
	switch name {
	case "detail":
		f.Detail = v
	case "related":
		f.Related = v
	case "milestone":
		f.Milestone = v
	case "repos":
		f.Repos = v
	case "subsystem":
		f.Subsystem = v
	case "captured":
		f.Captured = v
	case "since":
		f.Since = v
	}
}

// ---- field scanning -------------------------------------------------------
//
// The Python field patterns are `\bCap:\s*(.+?)(?=\s+(?:Other|...):|$)` and
// friends. RE2 has no lookahead, so matchValue replays the backtracking by
// hand: \s* is greedy (it may swallow newlines) and gives characters back one
// at a time, the lazy group then grows one non-newline character at a time,
// and the first position where the lookahead holds ends the match.

// othersOf is every field name except cap.
func othersOf(cap string) []string {
	out := make([]string, 0, len(FieldNames)-1)
	for _, n := range FieldNames {
		if n != cap {
			out = append(out, n)
		}
	}
	return out
}

// capitalize is str.capitalize for the ASCII field names.
func capitalize(s string) string { return strings.ToUpper(s[:1]) + strings.ToLower(s[1:]) }

// lookahead is `(?=\s+(?:others):|$)` at offset e. Python's $ (no MULTILINE)
// matches at the end or just before a final "\n".
func lookahead(line string, e int, others []string) bool {
	if e == len(line) || (e == len(line)-1 && line[e] == '\n') {
		return true
	}
	i := e
	for i < len(line) {
		r, n := utf8.DecodeRuneInString(line[i:])
		if !pystr.IsSpace(r) {
			break
		}
		i += n
	}
	if i == e {
		return false
	}
	for _, o := range others {
		if strings.HasPrefix(line[i:], o+":") {
			return true
		}
	}
	return false
}

// matchValue matches `\s*(.+?)(?=LOOKAHEAD)` starting at offset a, right after
// "Cap:". It returns where the group starts and ends.
func matchValue(line string, a int, others []string) (gs, e int, ok bool) {
	return matchValueMin(line, a, others, 0)
}

// matchValueMin is matchValue for a `\s{minWS,}` in front of the group: the
// `Related:\s+` of remove_id_from_related_field wants minWS 1.
func matchValueMin(line string, a int, others []string, minWS int) (gs, e int, ok bool) {
	// Rune boundaries inside the whitespace run that \s* can consume.
	bounds := []int{a}
	for i := a; i < len(line); {
		r, n := utf8.DecodeRuneInString(line[i:])
		if !pystr.IsSpace(r) {
			break
		}
		i += n
		bounds = append(bounds, i)
	}
	for k := len(bounds) - 1; k >= minWS; k-- {
		gs := bounds[k]
		if gs >= len(line) || line[gs] == '\n' {
			continue // (.+?) needs at least one non-newline character
		}
		_, n := utf8.DecodeRuneInString(line[gs:])
		for e := gs + n; ; {
			if lookahead(line, e, others) {
				return gs, e, true
			}
			if e >= len(line) || line[e] == '\n' {
				break
			}
			_, n := utf8.DecodeRuneInString(line[e:])
			e += n
		}
	}
	return 0, 0, false
}

// wordBoundaryBefore is `\b` in front of a word character at offset pos.
func wordBoundaryBefore(line string, pos int) bool {
	if pos == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(line[:pos])
	return !pystr.IsWord(r)
}

// ParseFields extracts the trailing fields of a bullet line. Each field
// starts with "Name: " and runs until the next field marker or the end of the
// line, in any order (parse_todo_fields). The value is whitespace-stripped.
func ParseFields(line string) Fields {
	var f Fields
	for _, name := range FieldNames {
		others := othersOf(name)
		marker := name + ":"
		for from := 0; from < len(line); {
			i := strings.Index(line[from:], marker)
			if i < 0 {
				break
			}
			pos := from + i
			from = pos + 1
			if !wordBoundaryBefore(line, pos) {
				continue
			}
			if gs, e, ok := matchValue(line, pos+len(marker), others); ok {
				f.set(strings.ToLower(name), pystr.Strip(line[gs:e]))
				break
			}
		}
	}
	return f
}

// SetField sets, replaces or clears one trailing field on a bullet line
// (set_todo_field). field is lowercase and one of SettableFields; the error
// text is Python's ValueError message.
//
//   - present: the value is replaced in place, other fields untouched.
//   - absent: " Name: value" is appended; detail goes before the first other
//     field marker instead, to keep the canonical order.
//   - detail values are written backticked, as capture writes them.
//   - an empty or whitespace-only value drops the " Name: ..." segment,
//     delimited by the same next-field lookahead the parser uses.
func SetField(line, field, value string) (string, error) {
	settable := false
	for _, f := range SettableFields {
		settable = settable || f == field
	}
	if !settable {
		return "", errors.New(field + " is not a settable field; pick one of " + strings.Join(SettableFields, "/"))
	}
	cap := capitalize(field)
	marker := cap + ":"
	others := othersOf(cap)
	value = pystr.Strip(value)
	if field == "detail" && value != "" {
		value = "`" + pystr.Strip(strings.Trim(value, "`")) + "`"
	}
	present := false
	for from := 0; from < len(line); {
		i := strings.Index(line[from:], marker)
		if i < 0 {
			break
		}
		pos := from + i
		from = pos + 1
		if !wordBoundaryBefore(line, pos) {
			continue
		}
		if r, _ := utf8.DecodeRuneInString(line[pos+len(marker):]); pystr.IsSpace(r) && pos+len(marker) < len(line) {
			present = true
			break
		}
	}

	if value == "" {
		if !present {
			return pystr.Rstrip(line), nil
		}
		return pystr.Rstrip(dropField(line, marker, others)), nil
	}
	if present {
		// `(Cap:\s*)(.+?)(?=...)`, first match, no \b in front.
		for from := 0; from < len(line); {
			i := strings.Index(line[from:], marker)
			if i < 0 {
				break
			}
			pos := from + i
			from = pos + 1
			if gs, e, ok := matchValue(line, pos+len(marker), others); ok {
				return line[:gs] + value + line[e:], nil
			}
		}
		return line, nil
	}
	if field == "detail" {
		if at := firstMarker(line, others); at >= 0 {
			return line[:at] + " " + cap + ": " + value + line[at:], nil
		}
	}
	return pystr.Rstrip(line) + " " + cap + ": " + value, nil
}

// firstMarker is the offset of the first `\s(?:others):` match, -1 if none.
func firstMarker(line string, others []string) int {
	for i := 0; i < len(line); {
		r, n := utf8.DecodeRuneInString(line[i:])
		if pystr.IsSpace(r) {
			for _, o := range others {
				if strings.HasPrefix(line[i+n:], o+":") {
					return i
				}
			}
		}
		i += n
	}
	return -1
}

// dropField removes every `\s+Cap:\s*.+?(?=...)` segment, as re.sub does.
func dropField(line, marker string, others []string) string {
	return dropFieldMin(line, marker, others, 0)
}

// dropFieldMin is dropField with `\s{minWS,}` between the marker and its value.
func dropFieldMin(line, marker string, others []string, minWS int) string {
	var b strings.Builder
	copied := 0
	for s := 0; s < len(line); {
		r, n := utf8.DecodeRuneInString(line[s:])
		if !pystr.IsSpace(r) {
			s += n
			continue
		}
		run := s
		for run < len(line) {
			r, n := utf8.DecodeRuneInString(line[run:])
			if !pystr.IsSpace(r) {
				break
			}
			run += n
		}
		if strings.HasPrefix(line[run:], marker) {
			if _, e, ok := matchValueMin(line, run+len(marker), others, minWS); ok {
				b.WriteString(line[copied:s])
				copied, s = e, e
				continue
			}
		}
		s += n
	}
	b.WriteString(line[copied:])
	return b.String()
}

// ---- open and done bullets ------------------------------------------------

const sp = "[" + pystr.SpaceClass + "]"

// openRe is open_bullet_re(): `- **[B07] [P1] Title.** rest`, anchored at the
// start. \d is \p{Nd} to match Python's Unicode digits.
var openRe = regexp.MustCompile(`(?m)\A- \*\*\[([` + ItemLetters + `]\p{Nd}+)\](?:` + sp + `+\[([^\]]+)\])?` + sp + `+([^*]+?)\*\*(.*)$`)

// Bullet is one parsed open bullet line.
type Bullet struct {
	ID    string // "B07"
	Tag   string // "P1", "Major", "" when the bullet has none
	Title string // without the trailing "."
	Rest  string // everything after the closing "**": description and fields
}

// ParseOpen parses an open bullet line (`- **[B07] [P1] Title.** ...`). Done
// lines, which start `- ~~**[`, do not match.
func ParseOpen(line string) (Bullet, bool) {
	m := openRe.FindStringSubmatch(strings.TrimRight(line, "\n"))
	if m == nil {
		return Bullet{}, false
	}
	return Bullet{
		ID:    m[1],
		Tag:   pystr.Strip(m[2]),
		Title: pystr.Strip(strings.TrimRight(pystr.Strip(m[3]), ".")),
		Rest:  pystr.Strip(m[4]),
	}, true
}

// Done is one parsed done line.
type Done struct {
	ID     string // from the leading "**[ID]" of Inner, "" if absent
	Inner  string // the text between ~~ and ~~: the original bullet without "- "
	Date   string // YYYY-MM-DD
	Hash   string // short commit hash, or "#N" for an issue
	Reason string // done (no suffix), handed-off, blocked or dropped
	Note   string // text after "reason: ", "" when absent
}

// doneRe is _DONE_LINE_RE.
var doneRe = regexp.MustCompile("(?m)\\A- ~~(.+?)~~ Done (\\p{Nd}{4}-\\p{Nd}{2}-\\p{Nd}{2}) \\[`([^`]+)`\\]" +
	`(?: \((handed-off|blocked|dropped)(?:: (.*))?\))?` + sp + `*$`)

var doneIDRe = regexp.MustCompile(`\A\*\*\[([A-Z]\p{Nd}+)\]`)

// ParseDone parses a done line, `- ~~**[B07] ...**~~ Done DATE [`hash`]`
// with an optional "(reason)" or "(reason: note)" suffix.
func ParseDone(line string) (Done, bool) {
	m := doneRe.FindStringSubmatch(strings.TrimRight(line, "\n"))
	if m == nil {
		return Done{}, false
	}
	d := Done{Inner: m[1], Date: m[2], Hash: m[3], Reason: m[4], Note: m[5]}
	if id := doneIDRe.FindStringSubmatch(m[1]); id != nil {
		d.ID = id[1]
	}
	if d.Reason == "" {
		d.Reason = "done"
	}
	return d, true
}

// ClosureReasons are the reasons a done line can carry (CLOSURE_REASONS).
var ClosureReasons = []string{"done", "handed-off", "blocked", "dropped"}

// FormatDone turns an open bullet line into its done line,
// `- ~~INNER~~ Done DATE [`hash`]`. Any reason other than "done" appends
// " (reason)" or " (reason: note)"; the note is ignored for "done". As in
// Python an empty reason is not defaulted, so callers pass "done". The error
// is Python's ValueError message.
func FormatDone(openLine, date, hash, reason, note string) (string, error) {
	if !strings.HasPrefix(openLine, "- ") {
		return "", errors.New("expected line starting with '- '")
	}
	line := "- ~~" + pystr.Rstrip(openLine[2:]) + "~~ Done " + date + " [`" + hash + "`]"
	if reason != "done" {
		if note != "" {
			line += " (" + reason + ": " + note + ")"
		} else {
			line += " (" + reason + ")"
		}
	}
	return line, nil
}

// ---- ids and origin lines -------------------------------------------------

// FindItemIDs lists the bracketed IDs ([B07], [F12]) in text, deduplicated, in
// order of first appearance (find_item_ids). letters "" means ItemLetters.
// An ID is one of letters followed by at least two digits.
func FindItemIDs(text, letters string) []string {
	if letters == "" {
		letters = ItemLetters
	}
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(text); {
		if text[i] != '[' {
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(text[i+1:])
		if !strings.ContainsRune(letters, r) || i+1 >= len(text) {
			i++
			continue
		}
		j := i + 1 + n
		digits := 0
		for j < len(text) {
			d, dn := utf8.DecodeRuneInString(text[j:])
			if !pystr.IsDigit(d) {
				break
			}
			j += dn
			digits++
		}
		if digits < 2 || j >= len(text) || text[j] != ']' {
			i++
			continue
		}
		id := text[i+1 : j]
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
		i = j + 1
	}
	return out
}

var (
	doneSuffixRe = regexp.MustCompile(sp + `*Done` + sp + `+\p{Nd}{4}-\p{Nd}{2}-\p{Nd}{2}` + sp + "+\\[`[^`]+`\\]" +
		`(?: \((?:handed-off|blocked|dropped)(?:: .*)?\))?` + sp + `*\z`)
	strikeRe = regexp.MustCompile(`\A~~(.+?)~~\z`)
)

// FindOrigin finds the bullet that introduces id in corpus (BACKLOG.md plus
// ARCHIVE.md): the `- **[ID] ...` line, not a "Related: [ID]" reference in
// another bullet (find_origin_bullet). line has the leading "- ", any ~~
// wrapper and any trailing "Done DATE [`hash`] (reason)" suffix removed.
// title is the text up to the first "." after the ID and tag; it is "" where
// Python returns None, which also covers a title that is all whitespace.
func FindOrigin(corpus, id string) (line, title string, ok bool) {
	re := regexp.MustCompile(`(?m)^- (?:~~)?\*\*\[` + regexp.QuoteMeta(id) + `\].*$`)
	m := re.FindString(corpus)
	if m == "" {
		return "", "", false
	}
	line = strings.TrimPrefix(pystr.Strip(m), "- ")
	line = doneSuffixRe.ReplaceAllString(line, "")
	if s := strikeRe.FindStringSubmatch(line); s != nil {
		line = s[1]
	}
	tre := regexp.MustCompile(`\[` + regexp.QuoteMeta(id) + `\](?:` + sp + `+\[[^\]]+\])?` + sp + `+([^.\n]+)\.`)
	if t := tre.FindStringSubmatch(line); t != nil {
		title = pystr.Strip(t[1])
	}
	return line, title, true
}

// OpenEntry is one open bullet found by OpenBullets.
type OpenEntry struct {
	ID      string // "B07"
	Line    string // the bullet, whitespace-stripped
	Section string // "Bugs", "Features" or "Tasks"
	Fields  Fields
}

// OpenBullets lists every open bullet in the open sections of content
// (typically BACKLOG.md), in section order, skipping the Completed section
// and any line that is not an open bullet (iter_open_bullets).
func OpenBullets(content string) []OpenEntry {
	var out []OpenEntry
	for _, name := range OpenSections {
		s, e, ok := section.Find(content, name)
		if !ok {
			continue
		}
		for _, raw := range pystr.Splitlines(content[s:e]) {
			line := pystr.Strip(raw)
			b, ok := ParseOpen(line)
			if !ok {
				continue
			}
			out = append(out, OpenEntry{ID: b.ID, Line: line, Section: name, Fields: ParseFields(line)})
		}
	}
	return out
}
