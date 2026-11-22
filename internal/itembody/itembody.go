// Package itembody owns the headings an item body may carry that rota reads or
// writes, including per-kind templates, Depends on, Files, Touches and Capabilities. The writer (rota item create) and the reader
// (the round readiness check) both go through here, so the grammar is spelled
// once and pinned by a round-trip test.
package itembody

import (
	"github.com/l4ci/rota/internal/bulletfmt"
	"github.com/l4ci/rota/internal/section"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// DependsHeadRe matches a "Depends on" heading of any level, any case.
	DependsHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+depends[ \t]+on[ \t]*$`)
	// FilesHeadRe matches a "Files" or "Files touched" heading.
	FilesHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+files(?:[ \t]+touched)?[ \t]*$`)

	// TouchesHeadRe matches a "Touches" heading: the symbols, endpoints,
	// schemas, migrations and config keys an item changes.
	TouchesHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+touches[ \t]*$`)

	// CapabilitiesHeadRe matches a "Capabilities" heading: the contracts a
	// milestone changes, one per bullet, optionally tagged.
	CapabilitiesHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+capabilities[ \t]*$`)

	// PointersHeadRe matches a "Pointers" heading: the files an issue points at.
	PointersHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+pointers[ \t]*$`)

	anyHeadRe = regexp.MustCompile(`(?m)^#{1,6}[ \t]`)
)

// sectionEnd is the one end-of-section rule: a section whose heading starts at
// headStart and whose text starts at from runs to the next heading of the same
// or a higher level, so deeper subsections belong to it. It returns the index
// in text where the section ends, len(text) when nothing ends it. Section (the
// reader) and SetSection (the writer) both use it, so what one writes the
// other reads back whole.
func sectionEnd(text string, headStart, from int) int {
	level := strings.IndexFunc(text[headStart:], func(r rune) bool { return r != '#' })
	if n := regexp.MustCompile(`(?m)^#{1,` + strconv.Itoa(level) + `}[ \t]`).FindStringIndex(text[from:]); n != nil {
		return from + n[0]
	}
	return len(text)
}

// Section is the text under the first heading matching head, up to the next
// heading of the same or a higher level (see sectionEnd). ok is false when
// there is none.
func Section(text string, head *regexp.Regexp) (string, bool) {
	loc := head.FindStringIndex(text)
	if loc == nil {
		return "", false
	}
	return text[loc[1]:sectionEnd(text, loc[0], loc[1])], true
}

// Sections is every section under head, in order. A bundle's issue bodies are
// joined, so the joined text can hold one Files section per issue.
func Sections(text string, head *regexp.Regexp) []string {
	var out []string
	for {
		sec, ok := Section(text, head)
		if !ok {
			return out
		}
		out = append(out, sec)
		text = text[head.FindStringIndex(text)[1]+len(sec):]
	}
}

// Entry normalises the text of one bullet: spaces and surrounding backticks
// trimmed, lower-cased. It returns "" for an empty entry.
func Entry(line string) string {
	return strings.ToLower(strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "`")))
}

// Entries lists the bullet entries of a section, normalised by Entry,
// deduplicated and sorted. It returns nil when there are none.
func Entries(section string) []string {
	var entries []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		item, ok := bulletfmt.CutMarker(line, bulletfmt.DashStar)
		if !ok {
			continue
		}
		e := Entry(item)
		if e == "" {
			continue
		}
		entries = append(entries, e)
	}
	return SortedEntries(entries)
}

// SortedEntries deduplicates and sorts entries already normalised by Entry.
// It returns nil when there are none and does not modify entries.
func SortedEntries(entries []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range entries {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	sort.Strings(out)
	return out
}

// HasDependsOn reports whether body already carries a Depends on heading.
func HasDependsOn(body []byte) bool { return DependsHeadRe.Match(body) }

// AppendDependsOn adds a "## Depends on" section, one bullet per reference,
// after body.
func AppendDependsOn(body []byte, refs []string) []byte {
	return appendBullets(body, "Depends on", refs)
}

func appendBullets(body []byte, heading string, items []string) []byte {
	var b strings.Builder
	if t := strings.TrimRight(string(body), "\n"); t != "" {
		b.WriteString(t + "\n\n")
	}
	b.WriteString("## " + heading + "\n\n")
	for _, it := range items {
		b.WriteString("- " + it + "\n")
	}
	return []byte(b.String())
}

// SetSection replaces the content of the "## name" section of text, or appends
// the section when text has none. The heading may be of any level and any
// case; a replaced heading keeps its spelling, a new one is written as
// "## Name". The acceptance section is the exception: it is found by
// AcceptanceHeadRe, the grammar readiness reads, and its heading is rewritten
// to AcceptanceHeading so the criteria numbering finds it. The section ends where sectionEnd says, the rule Section reads by. Everything
// outside it is untouched. content is trimmed of surrounding newlines.
func SetSection(text, name, content string) string {
	content = strings.Trim(content, "\n")
	acceptance := strings.EqualFold(name, "acceptance")
	head := section.HeadingRe(name)
	if acceptance {
		head = AcceptanceHeadRe
	}
	loc := head.FindStringIndex(text)
	if loc != nil && acceptance {
		if i := strings.IndexByte(text[loc[1]:], '\n'); i >= 0 {
			loc[1] += i
		} else {
			loc[1] = len(text)
		}
	}
	if loc == nil {
		r, n := utf8.DecodeRuneInString(name)
		title := string(unicode.ToUpper(r)) + name[n:]
		sec := "## " + title + "\n\n" + content + "\n"
		if t := strings.TrimRight(text, "\n"); t != "" {
			return t + "\n\n" + sec
		}
		return sec
	}
	end := sectionEnd(text, loc[0], loc[1])
	heading := text[:loc[1]]
	if acceptance {
		heading = text[:loc[0]] + AcceptanceHeading
	}
	sec := heading + "\n\n" + content + "\n"
	if end < len(text) {
		sec += "\n"
	}
	return sec + text[end:]
}

// lineSuffixRe matches a trailing line position on a path token: :N, :N-M or
// :N,M (and runs of them), which names a place in the file, not another path.
var lineSuffixRe = regexp.MustCompile(`(?::\d+(?:-\d+)?(?:,\d+(?:-\d+)?)*)+$`)

// BulletPaths reads the paths of each bullet in a section: every backticked
// token and every bare word. pathLike keeps only entries with a slash or glob;
// otherwise (a Files section) a bullet with no backticks gives its first word.
func BulletPaths(section string, pathLike bool) []string {
	var out []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		item, ok := bulletfmt.CutMarker(line, bulletfmt.DashStar)
		if !ok {
			continue
		}
		line = strings.TrimSpace(item)
		line = strings.TrimPrefix(strings.TrimPrefix(line, "[ ] "), "[x] ")
		// backticked tokens, plus the bare words outside the backticks
		var toks []string
		bare := line
		for rest := line; ; {
			a := strings.Index(rest, "`")
			if a < 0 {
				break
			}
			b := strings.Index(rest[a+1:], "`")
			if b < 0 {
				break
			}
			tok := rest[a+1 : a+1+b]
			toks = append(toks, tok)
			bare = strings.Replace(bare, "`"+tok+"`", " ", 1)
			rest = rest[a+b+2:]
		}
		words := strings.Fields(bare)
		if !pathLike && len(toks) == 0 && len(words) > 0 {
			words = words[:1] // a Files bullet with no backticks names its path first
		} else if !pathLike {
			words = nil
		}
		for _, w := range words {
			toks = append(toks, strings.Trim(w, ",;()"))
		}
		for _, tok := range toks {
			tok = lineSuffixRe.ReplaceAllString(strings.TrimSpace(tok), "")
			if tok == "" || (pathLike && !strings.ContainsAny(tok, "/*")) {
				continue
			}
			out = append(out, tok)
		}
	}
	return out
}
