// Package skills installs the skill set embedded in the rota binary for Claude
// Code and Codex (docs/design/contract/, F6a). Each skill becomes a
// self-contained directory: its markdown files plus the references it cites,
// so a `references/x.md` link resolves against the skill's own directory.
// A manifest per root records what rota wrote, and rota touches nothing outside it.
package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	rota "github.com/l4ci/rota"
)

// Set is a skill set in its installed layout.
type Set struct {
	files  map[string][]byte // "<skill>/<path>" to content
	paths  []string          // sorted keys of files
	skills []string          // sorted skill names
	digest string
}

var (
	embeddedOnce sync.Once
	embedded     *Set
	embeddedErr  error
)

// Embedded is the set compiled into the binary.
func Embedded() (*Set, error) {
	embeddedOnce.Do(func() { embedded, embeddedErr = Load(rota.FS) })
	return embedded, embeddedErr
}

// refMention finds `references/<name>.md` in any spelling (`../references/x.md`
// and `references/x.md` alike); bareMention finds a sibling named without a
// directory, as one reference does for another.
var (
	refMention  = regexp.MustCompile(`references/([A-Za-z0-9][A-Za-z0-9._-]*\.md)`)
	bareMention = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._-]*\.md`)
)

// Load builds a Set from a tree with rota-*/*.md skills and references/*.md.
func Load(fsys fs.FS) (*Set, error) {
	refs := map[string][]byte{}
	entries, err := fs.ReadDir(fsys, "references")
	if err != nil {
		return nil, fmt.Errorf("skills: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".md" {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join("references", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("skills: %w", err)
		}
		refs[e.Name()] = b
	}
	top, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("skills: %w", err)
	}
	s := &Set{files: map[string][]byte{}}
	for _, d := range top {
		if !d.IsDir() || !strings.HasPrefix(d.Name(), "rota-") {
			continue
		}
		files := map[string][]byte{}
		list, err := fs.ReadDir(fsys, d.Name())
		if err != nil {
			return nil, fmt.Errorf("skills: %w", err)
		}
		for _, f := range list {
			if f.IsDir() || path.Ext(f.Name()) != ".md" {
				continue
			}
			b, err := fs.ReadFile(fsys, path.Join(d.Name(), f.Name()))
			if err != nil {
				return nil, fmt.Errorf("skills: %w", err)
			}
			files[f.Name()] = b
		}
		if _, ok := files["SKILL.md"]; !ok {
			continue
		}
		for name := range closure(files, refs) {
			files["references/"+name] = refs[name]
		}
		for p, b := range files {
			s.files[d.Name()+"/"+p] = b
		}
		s.skills = append(s.skills, d.Name())
	}
	if len(s.skills) == 0 {
		return nil, fmt.Errorf("skills: no rota-* skill found")
	}
	for p := range s.files {
		s.paths = append(s.paths, p)
	}
	sort.Strings(s.paths)
	sort.Strings(s.skills)
	h := sha256.New()
	for _, p := range s.paths {
		fmt.Fprintf(h, "%s\x00%s\n", p, hashBytes(s.files[p]))
	}
	s.digest = hex.EncodeToString(h.Sum(nil))
	return s, nil
}

// closure is the names of the references a skill's own files cite, plus the
// ones those references cite in turn.
func closure(skillFiles, refs map[string][]byte) map[string]bool {
	need := map[string]bool{}
	var queue []string
	add := func(name string) {
		if _, ok := refs[name]; ok && !need[name] {
			need[name] = true
			queue = append(queue, name)
		}
	}
	for _, b := range skillFiles {
		for _, m := range refMention.FindAllSubmatch(b, -1) {
			add(string(m[1]))
		}
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if name == "README.md" {
			continue // the index links every reference; installing it must not
		}
		b := refs[name]
		for _, m := range refMention.FindAllSubmatch(b, -1) {
			add(string(m[1]))
		}
		for _, m := range bareMention.FindAll(b, -1) {
			// README.md in prose means the project's README; only a
			// `references/README.md` citation installs the index.
			if string(m) != "README.md" {
				add(string(m))
			}
		}
	}
	return need
}

// Digest identifies the set without a version: the sha256 of the sorted
// "path NUL sha256(content) NL" lines over the files one install writes.
func (s *Set) Digest() string { return s.digest }

// Paths are the installed paths ("<skill>/<file>"), sorted.
func (s *Set) Paths() []string { return append([]string(nil), s.paths...) }

// Skills are the skill names, sorted.
func (s *Set) Skills() []string { return append([]string(nil), s.skills...) }

// File is the content of an installed path.
func (s *Set) File(p string) ([]byte, bool) {
	b, ok := s.files[p]
	return b, ok
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
