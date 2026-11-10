// Package pathglob is the one place that answers "does this repository-relative
// file match this configured path". Callers pick by what the configured string
// means:
//
//   - Match: a glob over the whole path. path.Match segments, plus ** as a
//     whole segment for any number of directories (knowledge path scopes, the
//     change tier policy).
//   - MatchEntry: a configured entry that is equal to the file, a directory the
//     file lies under, or a glob whose flavour the caller names: Recursive
//     (review paths, scope-drift declarations, lens paths,
//     ship.mergeApprovalPaths) or SingleLevel (round.sharedPaths).
//   - MatchUnder: a Match glob that may name any ancestor directory of the
//     file (scope-drift support paths).
//
// It is a leaf: it imports nothing but the standard library.
package pathglob

import (
	"fmt"
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

// Flavour names how the glob arm of MatchEntry reads `**`.
type Flavour int

const (
	// Recursive reads `**` as a whole segment matching any number of
	// directories (Match). ship.mergeApprovalPaths, review paths, lens paths
	// and scope-drift declarations use it.
	Recursive Flavour = iota
	// SingleLevel hands the entry to path.Match against the whole path, so
	// `**` is an ordinary segment wildcard like `*` and never crosses a `/`.
	// round.sharedPaths uses it: widening it would hide overlaps.
	SingleLevel
)

// MatchEntry reports whether file matches a configured entry: equal to it,
// under it as a directory, or matching it as a glob of the given flavour. A
// leading `./` and a trailing `/` on the entry are ignored; an empty entry
// matches nothing. A malformed single-level glob matches nothing.
func MatchEntry(f Flavour, entry, file string) bool {
	entry = strings.TrimSuffix(Clean(entry), "/")
	if entry == "" {
		return false
	}
	if file == entry || strings.HasPrefix(file, entry+"/") {
		return true
	}
	if f == SingleLevel {
		ok, err := path.Match(entry, file)
		return err == nil && ok
	}
	return Match(entry, file)
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

// Valid reports whether glob is one every configured glob key accepts: slash
// separated segments, each a path.Match pattern, or ** as a whole segment for
// any number of directories (leading, mid-path or trailing, as Match reads
// it). No leading slash, no empty segment, no braces. One trailing / is the
// directory marker (docs/) and is allowed, as MatchEntry trims it.
func Valid(glob string) error {
	if strings.TrimSpace(glob) == "" {
		return fmt.Errorf("glob must not be empty")
	}
	if strings.HasPrefix(glob, "/") {
		return fmt.Errorf("glob %q must be relative to the repository root, with no leading /", glob)
	}
	if strings.ContainsAny(glob, "{}") {
		return fmt.Errorf("glob %q uses braces, which are not supported: list each pattern on its own", glob)
	}
	for _, seg := range strings.Split(strings.TrimSuffix(glob, "/"), "/") {
		if seg == "" {
			return fmt.Errorf("glob %q has an empty path segment", glob)
		}
		if seg == "**" {
			continue
		}
		if strings.Contains(seg, "**") {
			return fmt.Errorf("glob %q puts ** inside a segment: ** must be a whole segment, as in docs/**", glob)
		}
		if _, err := path.Match(seg, ""); err != nil {
			return fmt.Errorf("glob %q is malformed: %v", glob, err)
		}
	}
	return nil
}
