package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/fsio"
)

// files keeps each plan in .rota/plans/<key>.md under root.
type files struct{ root string }

// Files is the file-mode store.
func Files(root string) Store { return files{root} }

func (files) Digits() int { return backlog.FileIDDigits }

func (files) ItemOnly() bool { return false }

func path(root, key string) string { return filepath.Join(root, ".rota", "plans", key+".md") }

func notFound(key string) *exitcode.Error {
	return exitcode.Errf(exitcode.ExitResolution, "plan %s not found (.rota/plans/%s.md)", key, key)
}

// DesignRef is the path of the design file, which must exist.
func (s files) DesignRef(_ bool, design string) (string, error) {
	if !backlog.ValidID(design, backlog.FileIDDigits) {
		return "", exitcode.Errf(exitcode.ExitUsage, "--design must be an item ID like B07, got %q", design)
	}
	ref := ".rota/designs/" + design + ".md"
	if _, err := os.Stat(filepath.Join(s.root, ref)); err != nil {
		return "", exitcode.Errf(exitcode.ExitResolution, "--design file not found: %s", ref)
	}
	return ref, nil
}

func (s files) Create(milestone, unit string, render func(unit string) string) (key string, err error) {
	dir := filepath.Join(s.root, ".rota", "plans")
	// One lock per milestone for every S-unit, minted or explicit, so the
	// existence check and the minted number cannot race; an item plan locks
	// its own key.
	lockPath := path(s.root, milestone+"-slice")
	if unit != "" && !strings.HasPrefix(unit, "S") {
		lockPath = path(s.root, milestone+"-"+unit)
	}
	err = fsio.Locked(lockPath, fsio.LockTimeout, func() error {
		if unit == "" {
			unit = fmt.Sprintf("S%02d", nextSlice(dir, milestone))
		}
		key = milestone + "-" + unit
		p := path(s.root, key)
		if _, serr := os.Stat(p); serr == nil {
			return exitcode.Errf(exitcode.ExitRefused, ".rota/plans/%s.md already exists", key)
		}
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return err
		}
		return fsio.WriteFileAtomic(p, []byte(render(unit)))
	})
	return key, err
}

// nextSlice is 1 + the highest S<NN> among the milestone's plan files.
func nextSlice(dir, milestone string) int {
	files, _ := filepath.Glob(filepath.Join(dir, milestone+"-S*.md"))
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(milestone) + `-S(\d+)`)
	max := 0
	for _, f := range files {
		if m := re.FindStringSubmatch(strings.TrimSuffix(filepath.Base(f), ".md")); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > max {
				max = n
			}
		}
	}
	return max + 1
}

// Read is the stored plan, verbatim.
func (s files) Read(key string) (string, error) {
	b, err := os.ReadFile(path(s.root, key))
	if err != nil {
		return "", notFound(key)
	}
	return string(b), nil
}

func (s files) Replace(key, text string) (changed bool, err error) {
	p := path(s.root, key)
	if _, serr := os.Stat(p); serr != nil {
		return false, notFound(key)
	}
	err = fsio.Locked(p, fsio.LockTimeout, func() error {
		old, rerr := os.ReadFile(p)
		if rerr != nil {
			return notFound(key)
		}
		if string(old) == text {
			return nil
		}
		changed = true
		return fsio.WriteFileAtomic(p, []byte(text))
	})
	return
}

func (s files) Remove(key string) error {
	p := path(s.root, key)
	if _, err := os.Stat(p); err != nil {
		return notFound(key)
	}
	return fsio.Locked(p, fsio.LockTimeout, func() error {
		if err := os.Remove(p); err != nil {
			return notFound(key)
		}
		return nil
	})
}

// List reads .rota/plans/*.md in name order, optionally only one milestone's.
func (s files) List(milestone string) ([]Entry, error) {
	docs, err := artifact.ListDocs(filepath.Join(s.root, ".rota", "plans"))
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, d := range docs {
		ms := frontmatter.Str(d.FM, "milestone")
		if milestone != "" && ms != milestone {
			continue
		}
		out = append(out, entryOf(d.FM, d.Stem, ms, frontmatter.Str(d.FM, "unit"), "item"))
	}
	return out, nil
}

// entryOf reads a plan's frontmatter into a List row; stem, milestone, unit
// and kind are the defaults for the fields it lacks.
func entryOf(fm map[string]any, stem, milestone, unit, kind string) Entry {
	e := Entry{Key: orDefault(frontmatter.Str(fm, "key"), stem), Milestone: milestone, Unit: unit,
		UnitKind: orDefault(frontmatter.Str(fm, "unitKind"), kind),
		Title:    frontmatter.Str(fm, "title"), Status: orDefault(frontmatter.Str(fm, "status"), "planned"),
		Created: frontmatter.Str(fm, "created"), Repos: []string{}}
	switch r := fm["repo"].(type) {
	case string:
		e.Repos = artifact.SplitCSV(r)
	case []string:
		e.Repos = append(e.Repos, r...)
	}
	return e
}
