// Package release holds the domain logic of the `rota release` verbs (A8, #52),
// ported from bin/hvlib_version.py and the hv-release-* helpers: the
// version-file registry, semver bumps, the changelog insert, commit notes,
// host detection and the release nudge.
package release

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/pystr"
)

// Failure classes of Bump; every other error is a plain read or write failure.
var (
	// ErrNotGreater: an explicit version that is not above the current one.
	ErrNotGreater = errors.New("version not greater")
	// ErrBadArg: an unknown kind or a bump that is neither a level nor X.Y.Z.
	ErrBadArg = errors.New("bad argument")
)

// Kind is one entry of the version-file registry.
type Kind struct {
	Name  string
	Files []string
}

// Registry lists the version-file kinds in detection order
// (hvlib_version.VERSION_KIND_REGISTRY).
var Registry = []Kind{
	{"plugin-json", []string{".claude-plugin/plugin.json"}},
	{"package-json", []string{"package.json"}},
	{"pyproject", []string{"pyproject.toml"}},
	{"cargo", []string{"Cargo.toml"}},
	{"plain", []string{"VERSION", "version.txt"}},
}

// KnownKinds is the registry's kind names in order.
func KnownKinds() []string {
	var out []string
	for _, k := range Registry {
		out = append(out, k.Name)
	}
	return out
}

func known(kind string) bool {
	for _, k := range Registry {
		if k.Name == kind {
			return true
		}
	}
	return false
}

// InferKind is hvlib.infer_version_kind: the kind by file name, "plain" for
// any name it does not know.
func InferKind(path string) string {
	if k := KindFromName(path); k != "" {
		return k
	}
	return "plain"
}

// KindFromName is the kind by file name, "" when the name is not a known
// manifest (a bare VERSION is plain; version.txt needs an explicit kind).
func KindFromName(path string) string {
	name := filepath.Base(path)
	switch {
	case name == "plugin.json":
		return "plugin-json"
	case strings.HasSuffix(name, ".json"):
		return "package-json"
	case name == "pyproject.toml":
		return "pyproject"
	case name == "Cargo.toml":
		return "cargo"
	case name == "VERSION":
		return "plain"
	}
	return ""
}

func tomlSection(section string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*\[` + regexp.QuoteMeta(section) + `\s*\]\s*$`)
}

var (
	nextSection = regexp.MustCompile(`(?m)^\s*\[`)
	versionLine = regexp.MustCompile(`(?m)^version\s*=\s*"([^"]*)"`)
	versionSub  = regexp.MustCompile(`(?m)^(version\s*=\s*)"[^"]*"`)
)

// ParseTOMLVersion is hvlib.parse_toml_version: the first `version = "..."`
// in the first listed section that has one.
func ParseTOMLVersion(text string, sections []string) (string, bool) {
	for _, s := range sections {
		m := tomlSection(s).FindStringIndex(text)
		if m == nil {
			continue
		}
		rest := text[m[1]:]
		block := rest
		if n := nextSection.FindStringIndex(rest); n != nil {
			block = rest[:n[0]]
		}
		if v := versionLine.FindStringSubmatch(block); v != nil {
			return v[1], true
		}
	}
	return "", false
}

var tomlSections = map[string][]string{
	"pyproject": {"project", "tool.poetry"},
	"cargo":     {"package"},
}

// parseVersion reads the version out of a file's text: ok is false when the
// file carries none (a parse failure is an error).
func parseVersion(kind, text string) (v string, ok bool, err error) {
	switch kind {
	case "plugin-json", "package-json":
		doc, err := jsonx.Decode([]byte(text))
		if err != nil {
			return "", false, err
		}
		obj, isObj := doc.(*jsonx.Object)
		if !isObj {
			return "", false, errors.New("top-level JSON value is not an object")
		}
		raw, found := obj.Get("version")
		if !found || raw == nil {
			return "", false, nil
		}
		s, isStr := raw.(string)
		if !isStr {
			return "", false, errors.New("version is not a string")
		}
		return s, true, nil
	case "pyproject", "cargo":
		v, ok := ParseTOMLVersion(text, tomlSections[kind])
		return v, ok, nil
	default: // plain: the first non-blank line
		for _, line := range pystr.Splitlines(text) {
			if s := pystr.Strip(line); s != "" {
				return s, true, nil
			}
		}
		return "", false, nil
	}
}

// ReadVersion is hvlib.get_version_or_die for the file rel under dir; error
// messages name rel, as the old helper's stderr did.
func ReadVersion(dir, rel, kind string) (string, error) {
	if !known(kind) {
		return "", fmt.Errorf("%s: unknown version kind: '%s'", rel, kind)
	}
	text, err := fsio.ReadText(filepath.Join(dir, rel))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%s: file not found", rel)
		}
		return "", fmt.Errorf("%s: %v", rel, err)
	}
	v, ok, err := parseVersion(kind, text)
	if err != nil {
		return "", fmt.Errorf("%s: %v", rel, err)
	}
	if !ok {
		return "", fmt.Errorf("%s: no version field found", rel)
	}
	return v, nil
}

// Detect finds the project's version file in dir: the config override when
// override is non-empty, else the first registry file that exists.
func Detect(dir, override string) (file, version, kind string, err error) {
	if override != "" {
		if _, err := os.Stat(filepath.Join(dir, override)); err != nil {
			return "", "", "", fmt.Errorf("release.versionFile = '%s' does not exist", override)
		}
		kind = InferKind(override)
		v, err := ReadVersion(dir, override, kind)
		return override, v, kind, err
	}
	for _, k := range Registry {
		for _, f := range k.Files {
			if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
				continue
			}
			v, err := ReadVersion(dir, f, k.Name)
			return f, v, k.Name, err
		}
	}
	return "", "", "", errors.New("no version file detected (run `rota config set release.versionFile <path>`)")
}

// WriteVersion writes version into the file rel under dir the way the old
// writers did: JSON through the ordered encoder, TOML by substituting the
// first version line of the section, plain as "<version>\n".
func WriteVersion(dir, rel, kind, version string) error {
	path := filepath.Join(dir, rel)
	switch kind {
	case "plugin-json", "package-json":
		doc := fsio.LoadJSON(path, jsonx.NewObject())
		obj, ok := doc.(*jsonx.Object)
		if !ok {
			return fmt.Errorf("%s: top-level JSON value is not an object", rel)
		}
		obj.Set("version", version)
		return fsio.WriteJSONAtomic(path, obj)
	case "pyproject", "cargo":
		text, err := fsio.ReadText(path)
		if err != nil {
			return err
		}
		for _, s := range tomlSections[kind] {
			sec := tomlSection(s).FindStringIndex(text)
			if sec == nil {
				continue
			}
			end := len(text)
			if n := nextSection.FindStringIndex(text[sec[1]:]); n != nil {
				end = sec[1] + n[0]
			}
			body := text[sec[1]:end]
			m := versionSub.FindStringSubmatchIndex(body)
			if m == nil {
				continue
			}
			body = body[:m[0]] + body[m[2]:m[3]] + `"` + version + `"` + body[m[1]:]
			return fsio.WriteFileAtomic(path, []byte(text[:sec[1]]+body+text[end:]))
		}
		return fmt.Errorf("%s: no version field in section", path)
	default:
		return fsio.WriteFileAtomic(path, []byte(version+"\n"))
	}
}

var semverRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// IsSemver reports whether s is a bare X.Y.Z.
func IsSemver(s string) bool { return semverRe.MatchString(s) }

func parseSemver(v string) [3]*big.Int {
	var out [3]*big.Int
	for i, p := range strings.Split(v, ".") {
		out[i], _ = new(big.Int).SetString(p, 10)
	}
	return out
}

// NextVersion applies bump (patch, minor, major or an explicit X.Y.Z) to
// current. An explicit version must be strictly greater (ErrNotGreater).
func NextVersion(current, bump string) (string, error) {
	if bump != "patch" && bump != "minor" && bump != "major" && !IsSemver(bump) {
		return "", fmt.Errorf("%w: bump must be patch|minor|major or X.Y.Z, got '%s'", ErrBadArg, bump)
	}
	if !IsSemver(current) {
		return "", fmt.Errorf("current version '%s' is not X.Y.Z semver", current)
	}
	cur := parseSemver(current)
	one := big.NewInt(1)
	switch bump {
	case "patch":
		return fmt.Sprintf("%s.%s.%s", cur[0], cur[1], new(big.Int).Add(cur[2], one)), nil
	case "minor":
		return fmt.Sprintf("%s.%s.0", cur[0], new(big.Int).Add(cur[1], one)), nil
	case "major":
		return fmt.Sprintf("%s.0.0", new(big.Int).Add(cur[0], one)), nil
	}
	nv := parseSemver(bump)
	greater := false
	for i := range nv {
		if c := nv[i].Cmp(cur[i]); c != 0 {
			greater = c > 0
			break
		}
	}
	if !greater {
		return "", fmt.Errorf("%w: explicit version '%s' must be strictly greater than current '%s'", ErrNotGreater, bump, current)
	}
	return bump, nil
}

// Bump is hv-release-bump-version: it validates, computes the next version
// of the file rel under dir and, unless dryRun, writes it.
func Bump(dir, rel, kind, bump string, dryRun bool) (string, error) {
	if !known(kind) {
		return "", fmt.Errorf("%w: unknown kind '%s' (known: %s)", ErrBadArg, kind, strings.Join(sortedKinds(), ", "))
	}
	if bump != "patch" && bump != "minor" && bump != "major" && !IsSemver(bump) {
		return "", fmt.Errorf("%w: bump must be patch|minor|major or X.Y.Z, got '%s'", ErrBadArg, bump)
	}
	if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
		return "", fmt.Errorf("file not found: %s", rel)
	}
	cur, err := ReadVersion(dir, rel, kind)
	if err != nil {
		return "", err
	}
	next, err := NextVersion(cur, bump)
	if err != nil || dryRun {
		return next, err
	}
	return next, WriteVersion(dir, rel, kind, next)
}

func sortedKinds() []string {
	k := KnownKinds()
	sort.Strings(k)
	return k
}
