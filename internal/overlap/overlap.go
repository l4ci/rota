// Package overlap owns footprint matching: which changed paths count when two
// branches are compared (the merge gate and the readiness check ask "do these
// touch the same files outside round.sharedPaths") and whether two footprints
// clash (Paths, Dirs). It works on path lists, so callers own the git diff and
// the policy is testable without git.
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
)

// Clean drops any leading `./` from a footprint or configured entry. A
// trailing `/` is kept: Paths and Dirs read it as the directory marker.
func Clean(entry string) string {
	for strings.HasPrefix(entry, "./") {
		entry = strings.TrimPrefix(entry, "./")
	}
	if entry == "." {
		return ""
	}
	return entry
}

// MatchPath reports whether the repo-relative file matches a listed entry:
// equal to it, under it as a directory, or matching it as a path.Match glob
// against the whole path.
func MatchPath(entry, file string) bool {
	entry = strings.TrimSuffix(Clean(entry), "/")
	if entry == "" {
		return false
	}
	if file == entry || strings.HasPrefix(file, entry+"/") {
		return true
	}
	ok, err := path.Match(entry, file)
	return err == nil && ok
}

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
// covers: the conflicting overlap of two branches' changes.
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

// Paths lists the paths two footprints share: equal paths, a path matching
// the other's glob, or a file under the other's directory.
func Paths(a, b []string) []string {
	hit := map[string]bool{}
	for _, x := range a {
		x = Clean(x)
		for _, y := range b {
			y = Clean(y)
			switch {
			case x == y:
				hit[x] = true
			case strings.HasSuffix(x, "/") && strings.HasPrefix(y, x):
				hit[y] = true
			case strings.HasSuffix(y, "/") && strings.HasPrefix(x, y):
				hit[x] = true
			default:
				if ok, _ := path.Match(x, y); ok {
					hit[y] = true
				} else if ok, _ := path.Match(y, x); ok {
					hit[x] = true
				}
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
			switch {
			case x == y:
			case strings.HasSuffix(x, "/") && strings.HasPrefix(y, x):
				hit[x] = true
			case strings.HasSuffix(y, "/") && strings.HasPrefix(x, y):
				hit[y] = true
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
