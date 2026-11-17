package extknowledge

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/l4ci/rota/internal/bulletfmt"
)

// ToolVersion is one tool a bullet was seen on, e.g. godot@4.6.
type ToolVersion struct{ Tool, Version string }

// Seen is the tools and versions in a stamp's seen: field.
type Seen []ToolVersion

var (
	seenToolRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*$`)
	seenVersionRe = regexp.MustCompile(`^[0-9a-z][0-9a-z.+-]*$`)
	seenSplitRe   = regexp.MustCompile(`[,\s]+`)
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
	// SeenErr is the ParseSeen error of a malformed seen: token, so a reader
	// that needs the versions can fail instead of reading the bullet as unseen.
	SeenErr error
}

// parseBullets lists the bullets of a topic file. Only the final <!-- … --> on
// a line is its stamp; earlier comment text belongs to the body.
func parseBullets(file string) []bullet {
	var out []bullet
	for i, l := range strings.Split(file, "\n") {
		if !strings.HasPrefix(l, "- ") {
			continue
		}
		p := bulletfmt.Parse(l)
		b := bullet{Line: i, Title: p.Title, Body: p.Body, Stamp: p.Stamp}
		parseStamp(&b, p.Stamp)
		out = append(out, b)
	}
	return out
}

// parseStamp fills date, source and seen from "<!-- date key:value … -->".
func parseStamp(b *bullet, comment string) {
	for _, tok := range bulletfmt.StampFields(comment) {
		switch {
		case bulletfmt.IsDate(tok):
			b.Date = tok
		case strings.HasPrefix(tok, "source:"):
			b.Source = strings.TrimPrefix(tok, "source:")
		case strings.HasPrefix(tok, "seen:"):
			b.Seen, b.SeenErr = ParseSeen(strings.TrimPrefix(tok, "seen:"))
		}
	}
}

// subIndexRe is one sub-index line of a split topic's README.md:
// "- [Name](<sub>.md) — hook". The path is matched loosely; a path that is not
// a plain slug is reported by Malformed and never followed.
var subIndexRe = regexp.MustCompile(`^- \[(` + indexNamePattern + `)\]\(([^)]*)\.md\)(?:\s+—\s+(.*))?\s*$`)

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
