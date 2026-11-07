package prompts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotatree"
)

var (
	ErrInvalid  = errors.New("invalid prompt")
	ErrCorrupt  = errors.New("damaged prompt store")
	ErrConflict = errors.New("prompt revision conflict")
	ErrUnknown  = errors.New("unknown prompt section")
	ErrRemoved  = errors.New("prompt section removed")
)

// Revision is an immutable saved choice against the builtin at save time.
// A nil override records adoption of that builtin.
type Revision struct {
	Revision       int     `json:"revision"`
	Baseline       string  `json:"baseline"`
	BaselineDigest string  `json:"baselineDigest"`
	Override       *string `json:"override"`
}

// View combines saved history with the currently installed builtin.
type View struct {
	Name           string     `json:"name"`
	Revision       int        `json:"revision"`
	Status         string     `json:"status"`
	Baseline       string     `json:"baseline"`
	BaselineDigest string     `json:"baselineDigest"`
	Default        string     `json:"default"`
	Template       string     `json:"template"`
	Override       *string    `json:"override"`
	Effective      string     `json:"effective"`
	History        []Revision `json:"history"`
}

type document struct {
	Version  int                   `json:"version"`
	Sections map[string][]Revision `json:"sections"`
}

// Store reads and updates repository-scoped prompt history.
type Store struct {
	root    string
	catalog map[string]Section
}

// Open constructs a store without reading or changing disk.
func Open(root string, catalog map[string]Section) Store {
	c := make(map[string]Section, len(catalog))
	for k, v := range catalog {
		c[k] = v
	}
	return Store{root: root, catalog: c}
}

func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

var placeholder = regexp.MustCompile(`\{\{[^{}]*\}\}`)

func validate(body string) error {
	if !utf8.ValidString(body) {
		return fmt.Errorf("%w: template is not valid UTF-8", ErrInvalid)
	}
	if strings.Count(body, "{{status}}") != 1 || strings.Count(body, "{{closing}}") != 1 {
		return fmt.Errorf("%w: {{status}} and {{closing}} must each occur exactly once", ErrInvalid)
	}
	for _, p := range placeholder.FindAllString(body, -1) {
		if p != "{{status}}" && p != "{{closing}}" {
			return fmt.Errorf("%w: unknown placeholder %s; only {{status}} and {{closing}} are allowed", ErrInvalid, p)
		}
	}
	rest := strings.ReplaceAll(strings.ReplaceAll(body, "{{status}}", ""), "{{closing}}", "")
	if strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
		return fmt.Errorf("%w: malformed placeholder", ErrInvalid)
	}
	return nil
}
func expand(body string, sec Section) string {
	return strings.NewReplacer("{{status}}", sec.Status, "{{closing}}", sec.Closing).Replace(body)
}
func (s Store) path() string { return rotatree.Prompts(s.root) }

// safe refuses symlinks for reads as well as writes, including the lock sidecar.
func (s Store) safe() error {
	for _, p := range []string{rotatree.Dir(s.root), s.path(), fsio.LockPath(s.path())} {
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s is a symlink", ErrCorrupt, p)
		}
	}
	return nil
}
func (s Store) read() (document, error) {
	d := document{Version: 1, Sections: map[string][]Revision{}}
	if err := s.safe(); err != nil {
		return d, err
	}
	b, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return d, nil
	}
	if err != nil {
		return d, err
	}
	if !utf8.Valid(b) {
		return d, fmt.Errorf("%w: prompts.json is not valid UTF-8", ErrCorrupt)
	}
	d = document{}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, fmt.Errorf("%w: prompts.json: %v", ErrCorrupt, err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return d, fmt.Errorf("%w: trailing prompts.json content", ErrCorrupt)
	}
	if d.Version != 1 || d.Sections == nil {
		return d, fmt.Errorf("%w: unsupported or malformed prompts.json", ErrCorrupt)
	}
	for name, h := range d.Sections {
		if name == "" || len(h) == 0 {
			return d, fmt.Errorf("%w: empty section history", ErrCorrupt)
		}
		for i, r := range h {
			if r.Revision != i+1 || r.BaselineDigest != digest(r.Baseline) {
				return d, fmt.Errorf("%w: corrupt revision in %q", ErrCorrupt, name)
			}
			if r.Override != nil {
				if err := validate(*r.Override); err != nil {
					return d, fmt.Errorf("%w: %q revision %d: %v", ErrCorrupt, name, r.Revision, err)
				}
			}
		}
	}
	return d, nil
}
func (s Store) view(name string, d document) (View, error) {
	sec, ok := s.catalog[name]
	h := d.Sections[name]
	if !ok && len(h) == 0 {
		return View{}, fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	v := View{Name: name, Status: "default", Default: expand(sec.Default, sec), Template: sec.Default, History: append([]Revision{}, h...)}
	if ok {
		v.Baseline = expand(sec.Default, sec)
		v.BaselineDigest = digest(v.Baseline)
		v.Effective = expand(sec.Default, sec)
	}
	if len(h) > 0 {
		r := h[len(h)-1]
		v.Revision = r.Revision
		v.Baseline = r.Baseline
		v.BaselineDigest = r.BaselineDigest
		v.Override = r.Override
		v.Status = "unchanged"
		if !ok {
			v.Status = "removed"
		} else if r.Override != nil && digest(expand(sec.Default, sec)) != r.BaselineDigest {
			v.Status = "changed"
		}
		if ok && r.Override != nil {
			v.Effective = expand(*r.Override, sec)
		}
	}
	return v, nil
}

func (s Store) List() ([]View, error) {
	d, err := s.read()
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for n := range s.catalog {
		names[n] = true
	}
	for n := range d.Sections {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	out := make([]View, 0, len(sorted))
	for _, n := range sorted {
		v, err := s.view(n, d)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (s Store) Show(name string) (View, error) {
	d, err := s.read()
	if err != nil {
		return View{}, err
	}
	return s.view(name, d)
}

// Save appends a revision if expect still names the current section revision.
func (s Store) Save(name, body string, expect int) (View, error) {
	if err := validate(body); err != nil {
		return View{}, err
	}
	return s.append(name, expect, func(_ []Revision, baseline string) (Revision, error) {
		return Revision{Baseline: baseline, Override: &body}, nil
	})
}

// Adopt appends a revision that follows the current builtin. Earlier custom
// revisions stay in the history and can be restored.
func (s Store) Adopt(name string, expect int) (View, error) {
	return s.append(name, expect, func(_ []Revision, baseline string) (Revision, error) {
		return Revision{Baseline: baseline}, nil
	})
}

// Restore appends a copy of revision rev, keeping the baseline it recorded so
// an upstream change since then still shows as changed.
func (s Store) Restore(name string, rev, expect int) (View, error) {
	return s.append(name, expect, func(h []Revision, _ string) (Revision, error) {
		if rev < 1 || rev > len(h) {
			return Revision{}, fmt.Errorf("%w: %s has no revision %d", ErrInvalid, name, rev)
		}
		r := h[rev-1]
		if r.Override != nil {
			o := *r.Override
			r.Override = &o
		}
		return r, nil
	})
}

// append adds the revision next builds once expect still names the current
// revision; next gets the history and the expanded builtin baseline.
func (s Store) append(name string, expect int, next func(h []Revision, baseline string) (Revision, error)) (View, error) {
	var out View
	if expect < 0 {
		return out, fmt.Errorf("%w: negative expected revision", ErrInvalid)
	}
	if err := s.safe(); err != nil {
		return out, err
	}
	err := fsio.Locked(s.path(), fsio.LockTimeout, func() error {
		d, err := s.read()
		if err != nil {
			return err
		}
		sec, ok := s.catalog[name]
		if !ok {
			if len(d.Sections[name]) > 0 {
				return fmt.Errorf("%w: %s", ErrRemoved, name)
			}
			return fmt.Errorf("%w: %s", ErrUnknown, name)
		}
		h := d.Sections[name]
		if len(h) != expect {
			return fmt.Errorf("%w: %s expected %d, current %d", ErrConflict, name, expect, len(h))
		}
		if err := validate(sec.Default); err != nil {
			return fmt.Errorf("built-in %s default: %v", name, err)
		}
		r, err := next(h, expand(sec.Default, sec))
		if err != nil {
			return err
		}
		r.Revision = len(h) + 1
		r.BaselineDigest = digest(r.Baseline)
		d.Sections[name] = append(h, r)
		if err := fsio.WriteJSONAtomic(s.path(), d); err != nil {
			return err
		}
		out, err = s.view(name, d)
		return err
	})
	return out, err
}
