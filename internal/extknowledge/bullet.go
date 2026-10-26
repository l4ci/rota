package extknowledge

import (
	"fmt"
	"regexp"
	"strings"
)

// ToolVersion is one tool a bullet was seen on, e.g. godot@4.6.
type ToolVersion struct{ Tool, Version string }

// Seen is the tools and versions in a stamp's seen: field.
type Seen []ToolVersion

var (
	seenToolRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*$`)
	seenVersionRe = regexp.MustCompile(`^[0-9a-z][0-9a-z.+-]*$`)
	seenSplitRe   = regexp.MustCompile(`[,\s]+`)
	dateRe        = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// ParseSeen parses "tool@version" entries separated by commas or spaces. Tool
// names and versions are lowercase and kept as given: a token with an
// uppercase letter is an error, not lowercased. An empty string is no entries.
// A bare tool, a missing side of the @, characters outside the name/version
// alphabets and a repeated tool are errors.
func ParseSeen(s string) (Seen, error) {
	var out Seen
	have := map[string]bool{}
	for _, tok := range seenSplitRe.Split(strings.TrimSpace(s), -1) {
		if tok == "" {
			continue
		}
		if tok != strings.ToLower(tok) {
			return nil, fmt.Errorf("%q has uppercase letters: tool names are lowercase, e.g. godot@4.6", tok)
		}
		tool, version, ok := strings.Cut(tok, "@")
		if !ok || !seenToolRe.MatchString(tool) || !seenVersionRe.MatchString(version) {
			return nil, fmt.Errorf("%q is not tool@version (tool [a-z0-9.+-], version like 4.6 or r186)", tok)
		}
		if have[tool] {
			return nil, fmt.Errorf("tool %q is listed twice", tool)
		}
		have[tool] = true
		out = append(out, ToolVersion{tool, version})
	}
	return out, nil
}

// String is the stamp form: tool@version,tool@version.
func (s Seen) String() string {
	parts := make([]string, len(s))
	for i, tv := range s {
		parts[i] = tv.Tool + "@" + tv.Version
	}
	return strings.Join(parts, ",")
}

// bullet is one "- " line of a topic file, split into its parts.
type bullet struct {
	Line   int    // zero-based line index in the file
	Title  string // text of the leading **title**, empty when the line has none
	Body   string // text between the title and the stamp
	Stamp  string // the final <!-- … --> comment, with its leading space; empty when none
	Date   string
	Source string
	Seen   Seen
}

// parseBullets lists the bullets of a topic file. Only the final <!-- … --> on
// a line is its stamp; earlier comment text belongs to the body.
func parseBullets(file string) []bullet {
	var out []bullet
	for i, l := range strings.Split(file, "\n") {
		if !strings.HasPrefix(l, "- ") {
			continue
		}
		b := bullet{Line: i}
		rest := l
		if tail := stampTail.FindString(l); tail != "" {
			rest = strings.TrimSuffix(l, tail)
			b.Stamp = tail
			parseStamp(&b, strings.TrimSpace(tail))
		}
		if m := titleRe.FindStringSubmatch(rest); m != nil {
			b.Title = m[1]
			rest = strings.TrimPrefix(rest, m[0])
			rest = strings.TrimPrefix(strings.TrimSpace(rest), "—")
		} else {
			rest = strings.TrimPrefix(rest, "- ")
		}
		b.Body = strings.TrimSpace(rest)
		out = append(out, b)
	}
	return out
}

// parseStamp fills date, source and seen from "<!-- date key:value … -->".
func parseStamp(b *bullet, comment string) {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(comment, "<!--"), "-->"))
	for _, tok := range strings.Fields(inner) {
		switch {
		case dateRe.MatchString(tok):
			b.Date = tok
		case strings.HasPrefix(tok, "source:"):
			b.Source = strings.TrimPrefix(tok, "source:")
		case strings.HasPrefix(tok, "seen:"):
			if s, err := ParseSeen(strings.TrimPrefix(tok, "seen:")); err == nil {
				b.Seen = s
			}
		}
	}
}

// subIndexRe is one sub-index line of a split topic's README.md:
// "- [Name](<sub>.md) — hook".
var subIndexRe = regexp.MustCompile(`^- \[([^\]]+)\]\(([^)/]+)\.md\)(?:\s+—\s+(.*))?\s*$`)

// isSubIndexLine reports whether a line of a split topic's README is one of
// its sub-index entries rather than a bullet.
func isSubIndexLine(l string) bool {
	return subIndexRe.MatchString(strings.TrimRight(l, " \t\r"))
}

// bullets lists the bullets of the topic's file. A split topic's README holds
// its sub-index lines among the "- " lines; those are not bullets.
func (t Topic) bullets(file string) []bullet {
	all := parseBullets(file)
	if !t.Split {
		return all
	}
	lines := strings.Split(file, "\n")
	out := all[:0:0]
	for _, b := range all {
		if !isSubIndexLine(lines[b.Line]) {
			out = append(out, b)
		}
	}
	return out
}
