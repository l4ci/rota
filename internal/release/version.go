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

// Kind is one entry of the version-file registry: how to recognise, read and
// write one kind of version file. Adding a kind is adding one entry here.
type Kind struct {
	Name  string
	Files []string
	// Match reports whether a file's base name is this kind.
	Match func(name string) bool
	// Read extracts the version from a file's text: ok is false when the
	// file carries none (a parse failure is an error).
	Read func(text string) (v string, ok bool, err error)
	// Write stores version into the file at path (rel names it in errors).
	Write func(path, rel, version string) error
}

// Registry lists the version-file kinds in detection order
// (hvlib_version.VERSION_KIND_REGISTRY). KindFromName takes the first Match.
var Registry = []Kind{
	{"plugin-json", []string{".claude-plugin/plugin.json"},
		func(n string) bool { return n == "plugin.json" }, readJSONVersion, writeJSONVersion},
	{"package-json", []string{"package.json"},
		func(n string) bool { return strings.HasSuffix(n, ".json") }, readJSONVersion, writeJSONVersion},
	{"pyproject", []string{"pyproject.toml"},
		func(n string) bool { return n == "pyproject.toml" },
		tomlReader("project", "tool.poetry"), tomlWriter("project", "tool.poetry")},
	{"cargo", []string{"Cargo.toml"},
		func(n string) bool { return n == "Cargo.toml" },
		tomlReader("package"), tomlWriter("package")},
	// version.txt needs an explicit kind: only a bare VERSION matches by name.
	{"plain", []string{"VERSION", "version.txt"},
		func(n string) bool { return n == "VERSION" }, readPlainVersion, writePlainVersion},
}

func lookup(kind string) (Kind, bool) {
	for _, k := range Registry {
		if k.Name == kind {
			return k, true
		}
	}
	return Kind{}, false
}

// KnownKinds is the registry's kind names in order.
func KnownKinds() []string {
	var out []string
	for _, k := range Registry {
		out = append(out, k.Name)
	}
	return out
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
	for _, k := range Registry {
		if k.Match(name) {
			return k.Name
		}
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

func readJSONVersion(text string) (string, bool, error) {
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
}

// writeJSONVersion goes through the ordered encoder.
func writeJSONVersion(path, rel, version string) error {
	doc := fsio.LoadJSON(path, jsonx.NewObject())
	obj, ok := doc.(*jsonx.Object)
	if !ok {
		return fmt.Errorf("%s: top-level JSON value is not an object", rel)
	}
	obj.Set("version", version)
	return fsio.WriteJSONAtomic(path, obj)
}

func tomlReader(sections ...string) func(string) (string, bool, error) {
	return func(text string) (string, bool, error) {
		v, ok := ParseTOMLVersion(text, sections)
		return v, ok, nil
	}
}

// tomlWriter substitutes the first version line of the first listed section
// that has one.
func tomlWriter(sections ...string) func(path, rel, version string) error {
	return func(path, _, version string) error {
		text, err := fsio.ReadText(path)
		if err != nil {
			return err
		}
		for _, s := range sections {
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
	}
}

// readPlainVersion is the first non-blank line.
func readPlainVersion(text string) (string, bool, error) {
	for _, line := range pystr.Splitlines(text) {
		if s := pystr.Strip(line); s != "" {
			return s, true, nil
		}
	}
	return "", false, nil
}

func writePlainVersion(path, _, version string) error {
	return fsio.WriteFileAtomic(path, []byte(version+"\n"))
}

// ReadVersion is hvlib.get_version_or_die for the file rel under dir; error
// messages name rel, as the old helper's stderr did.
func ReadVersion(dir, rel, kind string) (string, error) {
	k, found := lookup(kind)
	if !found {
		return "", fmt.Errorf("%s: unknown version kind: '%s'", rel, kind)
	}
	text, err := fsio.ReadText(filepath.Join(dir, rel))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%s: file not found", rel)
		}
		return "", fmt.Errorf("%s: %v", rel, err)
	}
	v, ok, err := k.Read(text)
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
// first version line of the section, plain as "<version>\n". An unknown kind
// is written as plain.
func WriteVersion(dir, rel, kind, version string) error {
	k, ok := lookup(kind)
	if !ok {
		k, _ = lookup("plain")
	}
	return k.Write(filepath.Join(dir, rel), rel, version)
}

var semverRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// IsSemver reports whether s is a bare X.Y.Z.
func IsSemver(s string) bool { return semverRe.MatchString(s) }

// checkBump is the one place the bump grammar lives: patch|minor|major or X.Y.Z.
func checkBump(bump string) error {
	if bump != "patch" && bump != "minor" && bump != "major" && !IsSemver(bump) {
		return fmt.Errorf("%w: bump must be patch|minor|major or X.Y.Z, got '%s'", ErrBadArg, bump)
	}
	return nil
}

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
	if err := checkBump(bump); err != nil {
		return "", err
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
	if _, ok := lookup(kind); !ok {
		return "", fmt.Errorf("%w: unknown kind '%s' (known: %s)", ErrBadArg, kind, strings.Join(sortedKinds(), ", "))
	}
	if err := checkBump(bump); err != nil {
		return "", err
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
