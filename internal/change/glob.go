package change

import (
	"fmt"
	"path"
	"strings"
)

// ValidGlob reports whether g is a glob the change policy can match: slash
// separated segments, each a path.Match pattern, or ** for any number of
// directories. No leading slash, no empty segment, no braces.
func ValidGlob(g string) error {
	if strings.TrimSpace(g) == "" {
		return fmt.Errorf("glob must not be empty")
	}
	if strings.HasPrefix(g, "/") {
		return fmt.Errorf("glob %q must be relative to the repository root, with no leading /", g)
	}
	if strings.ContainsAny(g, "{}") {
		return fmt.Errorf("glob %q uses braces, which are not supported: list each pattern on its own", g)
	}
	for _, seg := range strings.Split(g, "/") {
		if seg == "" {
			return fmt.Errorf("glob %q has an empty path segment", g)
		}
		if seg == "**" {
			continue
		}
		if strings.Contains(seg, "**") {
			return fmt.Errorf("glob %q puts ** inside a segment: ** must be a whole segment, as in docs/**", g)
		}
		if _, err := path.Match(seg, ""); err != nil {
			return fmt.Errorf("glob %q is malformed: %v", g, err)
		}
	}
	return nil
}

// Match reports whether file, a repository-relative path, matches g. A
// pattern with no slash matches at the root only (*.md is README.md, not
// docs/a.md); a ** segment matches zero or more directories.
func Match(g, file string) bool {
	return matchSegments(strings.Split(g, "/"), strings.Split(file, "/"))
}

// matchSegments is the algorithm of internal/knowledge/paths.go.
func matchSegments(pattern, file []string) bool {
	if len(pattern) == 0 {
		return len(file) == 0
	}
	if pattern[0] == "**" {
		return matchSegments(pattern[1:], file) || (len(file) > 0 && matchSegments(pattern, file[1:]))
	}
	if len(file) == 0 {
		return false
	}
	ok, _ := path.Match(pattern[0], file[0])
	return ok && matchSegments(pattern[1:], file[1:])
}
