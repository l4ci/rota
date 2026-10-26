// Package toolver reads the versions of the tools a project runs from the
// manifests it keeps, so a knowledge bullet recorded on an older version can
// be flagged for recheck. Detection is a table of detector rows, one per
// manifest file; it reads files only and never runs a tool.
package toolver

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// detector reads one manifest file and returns the tools it pins, by tool name
// (the lowercase display name a knowledge stamp uses).
type detector struct {
	file  string
	parse func(content string) map[string]string
}

// detectors is every manifest Detect knows. Add a row to teach it a tool.
var detectors = []detector{
	{"project.godot", parseGodot},
}

// godotFeaturesRe is the engine version line of project.godot:
// config/features=PackedStringArray("4.6", "Forward Plus").
var godotFeaturesRe = regexp.MustCompile(`^config/features=PackedStringArray\(\s*"(\d+\.\d+)"`)

func parseGodot(content string) map[string]string {
	for _, l := range strings.Split(content, "\n") {
		if m := godotFeaturesRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			return map[string]string{"godot": m[1]}
		}
	}
	return nil
}

// Detect returns the tool versions found in dir and every parent up to root
// (inclusive). The nearest manifest wins per tool. A root that is empty or not
// an ancestor of dir limits the search to dir. Unreadable or malformed
// manifests yield no entry.
func Detect(dir, root string) map[string]string {
	out := map[string]string{}
	for _, d := range searchDirs(dir, root) {
		for _, det := range detectors {
			raw, err := os.ReadFile(filepath.Join(d, det.file))
			if err != nil {
				continue
			}
			for tool, v := range det.parse(string(raw)) {
				if _, ok := out[tool]; !ok {
					out[tool] = v
				}
			}
		}
	}
	return out
}

// searchDirs is dir, then its parents up to root.
func searchDirs(dir, root string) []string {
	if dir == "" {
		return nil
	}
	dirs := []string{dir}
	if root == "" {
		return dirs
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return dirs
	}
	for d := dir; d != root; {
		d = filepath.Dir(d)
		dirs = append(dirs, d)
	}
	return dirs
}

// Newer reports whether project is a newer major.minor than seen. Only the
// leading segments both versions have count, up to two, so "4" vs "4.2" and
// patch differences are not newer. Unparseable versions are never newer.
func Newer(seen, project string) bool {
	s, p := segments(seen), segments(project)
	n := min(len(s), len(p), 2)
	for i := 0; i < n; i++ {
		if p[i] != s[i] {
			return p[i] > s[i]
		}
	}
	return false
}

// segments is the numeric dot segments of a version after an optional v/r
// prefix; nil when it does not start with a number.
func segments(v string) []int {
	v = strings.TrimLeft(strings.TrimSpace(v), "vVrR")
	var out []int
	for _, part := range strings.Split(v, ".") {
		digits := part
		for i, r := range part {
			if r < '0' || r > '9' {
				digits = part[:i]
				break
			}
		}
		n, err := strconv.Atoi(digits)
		if err != nil {
			break
		}
		out = append(out, n)
		if digits != part {
			break
		}
	}
	return out
}
