// Package overlap decides which changed paths count when two branches are
// compared: the merge gate and the readiness check both ask "do these touch the
// same files outside round.sharedPaths". It works on path lists, so callers own
// the git diff and the policy is testable without git.
package overlap

import (
	"path"
	"sort"
	"strings"
)

// MatchPath reports whether the repo-relative file matches a listed entry:
// equal to it, under it as a directory, or matching it as a path.Match glob
// against the whole path.
func MatchPath(entry, file string) bool {
	entry = strings.TrimPrefix(strings.TrimSuffix(entry, "/"), "./")
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
