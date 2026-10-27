// Package itembody owns the headings an item body may carry that rota reads or
// writes, including per-kind templates, Depends on, Files, Touches and Capabilities. The writer (rota item create) and the reader
// (the round readiness check) both go through here, so the grammar is spelled
// once and pinned by a round-trip test.
package itembody

import (
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

	anyHeadRe = regexp.MustCompile(`(?m)^#{1,6}[ \t]`)
)

// Section is the text under the first heading matching head, up to the next
// heading of any level. ok is false when there is none.
func Section(text string, head *regexp.Regexp) (string, bool) {
	loc := head.FindStringIndex(text)
	if loc == nil {
		return "", false
	}
	rest := text[loc[1]:]
	if n := anyHeadRe.FindStringIndex(rest); n != nil {
		rest = rest[:n[0]]
	}
	return rest, true
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
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		e := Entry(line[2:])
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

// AppendFiles adds a "## Files" section, one bullet per path or glob, after
// body.
func AppendFiles(body []byte, paths []string) []byte {
	return appendBullets(body, "Files", paths)
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
// "## Name". The section runs to the next heading of the same or a higher level. Everything
// outside it is untouched. content is trimmed of surrounding newlines.
func SetSection(text, name, content string) string {
	content = strings.Trim(content, "\n")
	head := regexp.MustCompile(`(?mi)^#{1,6}[ \t]+` + regexp.QuoteMeta(name) + `[ \t]*$`)
	loc := head.FindStringIndex(text)
	if loc == nil {
		r, n := utf8.DecodeRuneInString(name)
		title := string(unicode.ToUpper(r)) + name[n:]
		sec := "## " + title + "\n\n" + content + "\n"
		if t := strings.TrimRight(text, "\n"); t != "" {
			return t + "\n\n" + sec
		}
		return sec
	}
	// The section runs to the next heading of the same or a higher level, so
	// deeper subsections belong to it.
	level := strings.IndexFunc(text[loc[0]:], func(r rune) bool { return r != '#' })
	end := len(text)
	if n := regexp.MustCompile(`(?m)^#{1,` + strconv.Itoa(level) + `}[ \t]`).FindStringIndex(text[loc[1]:]); n != nil {
		end = loc[1] + n[0]
	}
	sec := text[:loc[1]] + "\n\n" + content + "\n"
	if end < len(text) {
		sec += "\n"
	}
	return sec + text[end:]
}
