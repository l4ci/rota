// Package pathglob is the one place that answers "does this repository-relative
// file match this configured path". Callers pick by what the configured string
// means:
//
//   - Match: a glob over the whole path. path.Match segments, plus ** as a
//     whole segment for any number of directories (knowledge path scopes, the
//     change tier policy).
//   - MatchEntry: a configured entry that is equal to the file, a directory the
//     file lies under, or a Match glob (review paths, scope-drift declarations,
//     lens paths).
//   - MatchEntryFlat: MatchEntry where ** is not recursive but an ordinary
//     path.Match segment, the documented ship.mergeApprovalPaths dialect.
//   - MatchUnder: a Match glob that may name any ancestor directory of the
//     file (scope-drift support paths).
//
// It is a leaf: it imports nothing but the standard library.
package pathglob

import (
	"path"
	"strings"
)

// Clean drops any leading `./` from a configured entry. A trailing `/` is
// kept: it is the directory marker for footprint entries.
func Clean(entry string) string {
	for strings.HasPrefix(entry, "./") {
		entry = strings.TrimPrefix(entry, "./")
	}
	if entry == "." {
		return ""
	}
	return entry
}

// MatchEntry reports whether file matches a configured entry: equal to it,
// under it as a directory, or matching it as a Match glob. A leading `./` and a
// trailing `/` on the entry are ignored; an empty entry matches nothing.
func MatchEntry(entry, file string) bool { return matchEntry(entry, file, Match) }

// MatchEntryFlat is MatchEntry with ** read as a plain path.Match segment (it
// matches one directory level, not many). ship.mergeApprovalPaths keeps this
// dialect until a recursive ** there is decided separately.
func MatchEntryFlat(entry, file string) bool {
	return matchEntry(entry, file, func(pattern, file string) bool {
		ok, err := path.Match(pattern, file)
		return err == nil && ok
	})
}

func matchEntry(entry, file string, glob func(pattern, file string) bool) bool {
	entry = strings.TrimSuffix(Clean(entry), "/")
	if entry == "" {
		return false
	}
	return file == entry || strings.HasPrefix(file, entry+"/") || glob(entry, file)
}

// MatchUnder reports whether file, or any directory above it, matches the glob,
// so internal/*/testdata/golden/** covers internal/x/testdata/golden/a/b.txt.
func MatchUnder(pattern, file string) bool {
	segs := strings.Split(file, "/")
	for i := 1; i <= len(segs); i++ {
		if Match(pattern, strings.Join(segs[:i], "/")) {
			return true
		}
	}
	return false
}

// ValidEntry reports whether a configured glob is one the review path rules
// accept: path.Match syntax, with ** allowed only as a trailing /** segment.
func ValidEntry(glob string) bool {
	g := strings.TrimSuffix(glob, "/**")
	_, err := path.Match(g, "")
	return err == nil && !strings.Contains(g, "**")
}

// Match reports whether file matches the glob pattern. A pattern with no slash
// matches at the root only (*.md is README.md, not docs/a.md); a ** segment
// matches zero or more directories.
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
