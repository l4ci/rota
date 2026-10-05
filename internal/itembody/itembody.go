// Package itembody owns the headings an item body may carry that rota reads or
// writes: Depends on and Files. The writer (rota item create) and the reader
// (the round readiness check) both go through here, so the grammar is spelled
// once and pinned by a round-trip test.
package itembody

import (
	"regexp"
	"strings"
)

var (
	// DependsHeadRe matches a "Depends on" heading of any level, any case.
	DependsHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+depends[ \t]+on[ \t]*$`)
	// FilesHeadRe matches a "Files" or "Files touched" heading.
	FilesHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+files(?:[ \t]+touched)?[ \t]*$`)

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
