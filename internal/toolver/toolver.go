// Package toolver reads the versions of the tools a project runs from the
// manifests it keeps, so a knowledge bullet recorded on an older version can
// be flagged for recheck. Detection is a table of detector rows, one per
// manifest file; it reads files only and never runs a tool.
package toolver

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// detector reads one manifest file (file is a path pattern relative to the
// searched directory; * matches within one segment) and returns the tools it pins, by tool name
// (the lowercase display name a knowledge stamp uses).
type detector struct {
	file  string
	parse func(content string) map[string]string
}

// detectors is every manifest Detect knows. Add a row to teach it a tool.
var detectors = []detector{
	{"project.godot", parseGodot},
	{"package.json", parsePackageJSON},
	{"go.mod", parseGoMod},
	{"pyproject.toml", parsePyproject},
	{"uv.lock", parseUVLock},
	{"Package.swift", parsePackageSwift},
	{"project.yml", parseSwiftVersions},
	{"*.xcodeproj/project.pbxproj", parseSwiftVersions},
}

// npmAliases maps package names onto the tool names knowledge stamps use.
var npmAliases = map[string]string{
	"next":                 "next.js",
	"three":                "three.js",
	"@playwright/test":     "playwright",
	"@graphql-codegen/cli": "graphql-codegen",
}

var (
	specRe      = regexp.MustCompile(`^(\d+(?:\.\d+){0,2})`)
	goModRe     = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+(?:\.\d+)?)\s*$`)
	pyReqRe     = regexp.MustCompile(`(?m)^requires-python\s*=\s*["']([^"']*)["']`)
	pyBoundRe   = regexp.MustCompile(`(?:>=|~=|==|>)\s*(\d+(?:\.\d+)*)`)
	swiftToolRe = regexp.MustCompile(`swift-tools-version\s*:?\s*(\d+(?:\.\d+)*)`)
	swiftVerRe  = regexp.MustCompile(`SWIFT_VERSION\s*[:=]\s*"?(\d+(?:\.\d+)*)`)
	pkgNameRe   = regexp.MustCompile(`^name\s*=\s*"([^"]+)"`)
	pkgVerRe    = regexp.MustCompile(`^version\s*=\s*"([^"]+)"`)
	pep503Re    = regexp.MustCompile(`[-_.]+`)
)

// lowerBound is the version at the bottom of the first range of an npm spec:
// "^4.1.0 || ^5" is 4.1.0, ">=4 <6" is 4. Upper-bound-only specs, tags, URLs
// and workspace links give "".
func lowerBound(spec string) string {
	first, _, _ := strings.Cut(spec, "||")
	first = strings.TrimLeft(strings.TrimSpace(first), "^~>=v ")
	if m := specRe.FindStringSubmatch(first); m != nil {
		return m[1]
	}
	return ""
}

func parsePackageJSON(content string) map[string]string {
	var pkg struct {
		Dependencies     map[string]string `json:"dependencies"`
		DevDependencies  map[string]string `json:"devDependencies"`
		PeerDependencies map[string]string `json:"peerDependencies"`
		Engines          map[string]string `json:"engines"`
	}
	if json.Unmarshal([]byte(content), &pkg) != nil {
		return nil
	}
	out := map[string]string{}
	for _, deps := range []map[string]string{pkg.Dependencies, pkg.DevDependencies, pkg.PeerDependencies} {
		for name, spec := range deps {
			v := lowerBound(spec)
			if v == "" {
				continue
			}
			name = strings.ToLower(name)
			if alias, ok := npmAliases[name]; ok {
				name = alias
			}
			if _, dup := out[name]; !dup {
				out[name] = v
			}
		}
	}
	if v := lowerBound(pkg.Engines["node"]); v != "" {
		out["node"] = v
	}
	return out
}

func parseGoMod(content string) map[string]string {
	if m := goModRe.FindStringSubmatch(content); m != nil {
		return map[string]string{"go": m[1]}
	}
	return nil
}

func parsePyproject(content string) map[string]string {
	if m := pyReqRe.FindStringSubmatch(content); m != nil {
		if b := pyBoundRe.FindStringSubmatch(m[1]); b != nil {
			return map[string]string{"python": b[1]}
		}
	}
	return nil
}

// parseUVLock reads the name and version of each [[package]] block; uv.lock is
// TOML but only those two lines matter.
func parseUVLock(content string) map[string]string {
	out := map[string]string{}
	var name string
	for _, l := range strings.Split(content, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "[[package]]":
			name = ""
		case pkgNameRe.MatchString(l):
			name = strings.ToLower(pep503Re.ReplaceAllString(pkgNameRe.FindStringSubmatch(l)[1], "-"))
		case name != "" && pkgVerRe.MatchString(l):
			out[name] = pkgVerRe.FindStringSubmatch(l)[1]
			name = ""
		}
	}
	return out
}

func parsePackageSwift(content string) map[string]string {
	if m := swiftToolRe.FindStringSubmatch(content); m != nil {
		return map[string]string{"swift": m[1]}
	}
	return nil
}

// parseSwiftVersions returns the highest SWIFT_VERSION in a project.yml or
// project.pbxproj.
func parseSwiftVersions(content string) map[string]string {
	best := ""
	for _, m := range swiftVerRe.FindAllStringSubmatch(content, -1) {
		if best == "" || Newer(best, m[1]) || (!Newer(m[1], best) && len(segments(m[1])) > len(segments(best))) {
			best = m[1]
		}
	}
	if best == "" {
		return nil
	}
	return map[string]string{"swift": best}
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
			files, err := fs.Glob(os.DirFS(d), det.file)
			if err != nil {
				continue
			}
			for _, f := range files {
				raw, err := os.ReadFile(filepath.Join(d, f))
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
