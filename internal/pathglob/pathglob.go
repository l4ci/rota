// Package pathglob matches repository-relative paths against slash-separated
// globs: path.Match segments, plus ** as a whole segment for any number of
// directories. It is a leaf shared by the knowledge path scopes and the change
// tier policy.
package pathglob

import (
	"path"
	"strings"
)

// Match reports whether file matches the glob pattern.
func Match(pattern, file string) bool {
	return MatchSegments(strings.Split(pattern, "/"), strings.Split(file, "/"))
}

// MatchSegments matches already-split pattern and file segments.
func MatchSegments(pattern, file []string) bool {
	if len(pattern) == 0 {
		return len(file) == 0
	}
	if pattern[0] == "**" {
		return MatchSegments(pattern[1:], file) || (len(file) > 0 && MatchSegments(pattern, file[1:]))
	}
	if len(file) == 0 {
		return false
	}
	ok, _ := path.Match(pattern[0], file[0])
	return ok && MatchSegments(pattern[1:], file[1:])
}
