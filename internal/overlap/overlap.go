// Package overlap owns footprint matching. It holds two predicates that answer
// different questions about different inputs; pick by what you are comparing:
//
//   - Both compares two lists of CHANGED files (git diff output of two
//     branches) by exact path equality, minus round.sharedPaths. The merge
//     gate and the train use it: a real git conflict needs the same file
//     edited on both sides, so a glob or directory has no meaning there.
//   - Paths compares two declared FOOTPRINTS (an issue's pointers, an open
//     PR's files) where an entry may be a glob or a trailing-`/` directory.
//     The readiness check uses it to warn before work starts, when only the
//     declarations exist. Dirs reports which clashes came from a directory
//     rather than an identical entry.
//
// Merging them would be wrong both ways: Both on footprints misses globs and
// directories; Paths on diffs would treat a changed file named `a[1].go` or
// `*.md` as a pattern.
//
// Every entry goes through Clean first: leading `./` (repeated) is dropped,
// in MatchPath, Paths, Dirs and round.Footprint alike. One rule differs on
// purpose. MatchPath takes a configured entry (config, sharedPaths), where a
// slashless entry is also a directory, so a trailing `/` is optional. Paths
// and Dirs take item footprints, where only a trailing `/` marks a directory
// and a slashless entry is a file.
package overlap

import (
	"path"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/pathglob"
)

// Clean drops any leading `./` from a footprint or configured entry. A
// trailing `/` is kept: Paths and Dirs read it as the directory marker.
func Clean(entry string) string { return pathglob.Clean(entry) }

// MatchPath reports whether the repo-relative file matches a listed entry:
// equal to it, under it as a directory, or matching it as a path.Match glob
// against the whole path.
func MatchPath(entry, file string) bool { return pathglob.MatchEntryFlat(entry, file) }

// Shared reports whether file matches any of the shared entries.
func Shared(shared []string, file string) bool {
	for _, g := range shared {
		if MatchPath(g, file) {
			return true
		}
	}
	return false
}

// Filter drops the paths that match a shared entry, keeping order.
func Filter(paths, shared []string) []string {
	var kept []string
	for _, p := range paths {
		if !Shared(shared, p) {
			kept = append(kept, p)
		}
	}
	return kept
}

// Both returns the sorted paths present in both lists that no shared entry
// covers: the conflicting overlap of two branches' changes. It compares
// changed files by exact equality; see the package doc for Paths.
func Both(a, b, shared []string) []string {
	inA := map[string]bool{}
	for _, p := range a {
		inA[p] = true
	}
	seen := map[string]bool{}
	var both []string
	for _, p := range b {
		if inA[p] && !seen[p] && !Shared(shared, p) {
			seen[p] = true
			both = append(both, p)
		}
	}
	sort.Strings(both)
	return both
}

// dirCover reports whether x or y is a directory entry (trailing `/`) that
// the other lies under, returning that directory and the covered entry. x is
// tried first. It is the one place the directory-prefix rule lives.
func dirCover(x, y string) (dir, covered string, ok bool) {
	switch {
	case strings.HasSuffix(x, "/") && strings.HasPrefix(y, x):
		return x, y, true
	case strings.HasSuffix(y, "/") && strings.HasPrefix(x, y):
		return y, x, true
	}
	return "", "", false
}

// Paths lists the paths two declared footprints share: equal paths, a path
// matching the other's glob, or a file under the other's directory. For
// changed-file lists use Both.
func Paths(a, b []string) []string {
	hit := map[string]bool{}
	for _, x := range a {
		x = Clean(x)
		for _, y := range b {
			y = Clean(y)
			if x == y {
				hit[x] = true
			} else if _, covered, ok := dirCover(x, y); ok {
				hit[covered] = true
			} else if ok, _ := path.Match(x, y); ok {
				hit[y] = true
			} else if ok, _ := path.Match(y, x); ok {
				hit[x] = true
			}
		}
	}
	var out []string
	for p := range hit {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Dirs lists the directory entries (trailing slash) that made a and b
// overlap by matching a different path, not an identical directory. A clash
// that comes from one of these is a vague pointer, not two issues editing the
// same file.
func Dirs(a, b []string) []string {
	hit := map[string]bool{}
	for _, x := range a {
		x = Clean(x)
		for _, y := range b {
			y = Clean(y)
			if x == y {
				continue
			}
			if dir, _, ok := dirCover(x, y); ok {
				hit[dir] = true
			}
		}
	}
	var out []string
	for d := range hit {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
