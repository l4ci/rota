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

	// PointersHeadRe matches a "Pointers" heading: the files an issue points at.
	PointersHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+pointers[ \t]*$`)

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
// "## Name". The acceptance section is the exception: it is found by
// AcceptanceHeadRe, the grammar readiness reads, and its heading is rewritten
// to AcceptanceHeading so the criteria numbering finds it. The section runs to the next heading of the same or a higher level. Everything
// outside it is untouched. content is trimmed of surrounding newlines.
func SetSection(text, name, content string) string {
	content = strings.Trim(content, "\n")
	acceptance := strings.EqualFold(name, "acceptance")
	head := regexp.MustCompile(`(?mi)^#{1,6}[ \t]+` + regexp.QuoteMeta(name) + `[ \t]*$`)
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
	// The section runs to the next heading of the same or a higher level, so
	// deeper subsections belong to it.
	level := strings.IndexFunc(text[loc[0]:], func(r rune) bool { return r != '#' })
	end := len(text)
	if n := regexp.MustCompile(`(?m)^#{1,` + strconv.Itoa(level) + `}[ \t]`).FindStringIndex(text[loc[1]:]); n != nil {
		end = loc[1] + n[0]
	}
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

// BulletPaths reads the paths of each bullet in a section: every backticked
// token and every bare word. pathLike keeps only entries with a slash or glob;
// otherwise (a Files section) a bullet with no backticks gives its first word.
func BulletPaths(section string, pathLike bool) []string {
	var out []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "* ") {
			continue
		}
		line = strings.TrimSpace(line[2:])
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
			tok = strings.TrimSpace(tok)
			if tok == "" || (pathLike && !strings.ContainsAny(tok, "/*")) {
				continue
			}
			out = append(out, tok)
		}
	}
	return out
}
