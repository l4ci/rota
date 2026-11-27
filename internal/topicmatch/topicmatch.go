// Package topicmatch holds the three rules that map a requested topic name to
// stored topics. They differ on purpose, by operation and by store; each is a
// named function so the differences live in one place and one table test.
package topicmatch

import (
	"regexp"
	"strings"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify is the topic file name for a topic name.
func Slugify(name string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// Entry is one stored topic: its display name and its file slug.
type Entry struct{ Name, Slug string }

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Headings resolves want against the "## " headings of the flat KNOWLEDGE and
// DECISIONS files, which have no slugs. A heading equal to want
// (case-insensitive) wins alone; otherwise every heading containing want is
// returned, lowercased and deduplicated in order. Reads are forgiving so
// `query arch` finds "Architecture", and dedupe because the same heading can
// repeat across the files a caller merges.
func Headings(want string, names []string) []string {
	w := norm(want)
	if w == "" {
		return nil
	}
	var partial []string
	seen := map[string]bool{}
	for _, n := range names {
		l := strings.ToLower(n)
		if l == w {
			return []string{l}
		}
		if strings.Contains(l, w) && !seen[l] {
			seen[l] = true
			partial = append(partial, l)
		}
	}
	return partial
}

// exactIndex finds the entry w (normalised) names exactly: by name anywhere in
// the list first, then by slug. Reads and writes share it, so a slug match on an
// earlier entry never takes a write away from what a query of that name prints.
func exactIndex(w string, entries []Entry) (int, bool) {
	for i, e := range entries {
		if strings.ToLower(e.Name) == w {
			return i, true
		}
	}
	for i, e := range entries {
		if e.Slug == w {
			return i, true
		}
	}
	return -1, false
}

// ReadIndex resolves want against the external store's topic index for a read.
// An entry whose name or slug equals want (case-insensitive) wins alone, the
// name before the slug; otherwise every entry whose name contains want, in index order. It returns indexes into entries. It does not
// dedupe: index entries are distinct topics even when two names collide, and
// each one is a file to print. The slug is matched only for the exact hit, so
// a substring of a slug alone finds nothing.
func ReadIndex(want string, entries []Entry) []int {
	w := norm(want)
	if w == "" {
		return nil
	}
	if i, ok := exactIndex(w, entries); ok {
		return []int{i}
	}
	var partial []int
	for i, e := range entries {
		if strings.Contains(strings.ToLower(e.Name), w) {
			partial = append(partial, i)
		}
	}
	return partial
}

// WriteIndex resolves want against the external store's topic index for a
// write. Exact only, never a partial match: a write that guessed would change
// the wrong topic. Besides name and slug it accepts Slugify(want), so a
// spelled-out "Go Tests" reaches the "go-tests" file; that match ranks last.
// It returns the index into entries.
func WriteIndex(want string, entries []Entry) (int, bool) {
	if i, ok := exactIndex(norm(want), entries); ok {
		return i, true
	}
	slug := Slugify(want)
	for i, e := range entries {
		if e.Slug == slug {
			return i, true
		}
	}
	return -1, false
}
