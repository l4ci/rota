package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._+-]`)

// ExtractPrefix starts the name of the temp dir an extraction builds in before
// renaming it into place; `rota reap` matches a killed extraction by it.
const ExtractPrefix = ".extract-"

// CacheName is the directory name of a binary's extracted tree. A release
// version names it alone; a build with no release version (a checkout build)
// also carries the set's digest, since one version string then covers
// different skill text.
func CacheName(version, digest string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	name := unsafeName.ReplaceAllString(v, "_")
	if v == "" || v == "dev" || v == "(devel)" || strings.HasSuffix(v, "-dev") {
		if len(digest) > 12 {
			digest = digest[:12]
		}
		name += "-" + digest
	}
	return strings.Trim(name, ".-")
}

// CacheDir is where extracted trees live: <user cache>/rota/skills, with
// ROTA_CACHE_DIR (read through getenv) standing in for the user cache dir.
// "" when no cache dir is known.
func CacheDir(getenv func(string) string) string {
	base := getenv("ROTA_CACHE_DIR")
	if base == "" {
		var err error
		if base, err = os.UserCacheDir(); err != nil {
			return ""
		}
	}
	return filepath.Join(base, "rota", "skills")
}

// Extract writes the set's full tree (every skill with its sibling files,
// references and agents, plus <tree>/references/ holding every reference once,
// where a reference's own links (`../<skill>/SKILL.md`, the index's siblings)
// resolve as they do in the source) to <base>/<CacheName> and returns that directory. An
// existing tree is reused untouched. The tree is built in a temp directory next
// to the target and renamed into place, so a reader never sees half of it and
// two concurrent first calls both succeed with one tree.
func Extract(s *Set, version, base string) (string, error) {
	target := filepath.Join(base, CacheName(version, s.digest))
	if fi, err := os.Stat(target); err == nil && fi.IsDir() {
		return target, nil
	}
	if err := os.MkdirAll(base, 0o777); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(base, ExtractPrefix+"*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	write := func(p string, b []byte) error {
		abs := filepath.Join(tmp, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o777); err != nil {
			return err
		}
		return os.WriteFile(abs, b, 0o666)
	}
	for _, p := range s.paths {
		if err := write(p, s.files[p]); err != nil {
			return "", err
		}
		if _, rest, _ := strings.Cut(p, "/"); strings.HasPrefix(rest, "references/") {
			if err := write(rest, s.files[p]); err != nil {
				return "", err
			}
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		if fi, serr := os.Stat(target); serr == nil && fi.IsDir() {
			return target, nil // another process won the race
		}
		var le *os.LinkError
		if errors.As(err, &le) {
			return "", fmt.Errorf("extract %s: %w", target, le.Err)
		}
		return "", err
	}
	return target, nil
}
