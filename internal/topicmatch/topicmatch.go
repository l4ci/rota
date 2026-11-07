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

// ReadIndex resolves want against the external store's topic index for a read.
// An entry whose name or slug equals want (case-insensitive) wins alone;
// otherwise every entry whose name contains want, in index order. It returns indexes into entries. It does not
// dedupe: index entries are distinct topics even when two names collide, and
// each one is a file to print. The slug is matched only for the exact hit, so
// a substring of a slug alone finds nothing.
func ReadIndex(want string, entries []Entry) []int {
	w := norm(want)
	if w == "" {
		return nil
	}
	var partial []int
	for i, e := range entries {
		if strings.ToLower(e.Name) == w || e.Slug == w {
			return []int{i}
		}
		if strings.Contains(strings.ToLower(e.Name), w) {
			partial = append(partial, i)
		}
	}
	return partial
}

// WriteIndex resolves want against the external store's topic index for a
// write. Exact only, never a partial match: a write that guessed would change
// the wrong topic. Besides name and slug it accepts Slugify(want), so a
// spelled-out "Go Tests" reaches the "go-tests" file. It returns the index
// into entries.
func WriteIndex(want string, entries []Entry) (int, bool) {
	w := norm(want)
	for i, e := range entries {
		if strings.ToLower(e.Name) == w || e.Slug == w || e.Slug == Slugify(want) {
			return i, true
		}
	}
	return -1, false
}
