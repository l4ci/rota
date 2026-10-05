// Package stale lists stale entries of the backlog, the knowledge file and the
// subsystem map, as hv-staleness does.
package stale

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/section"
)

// Kinds are the values of --kind.
var Kinds = []string{"map", "knowledge", "todo"}

// Entry is one stale entry and the date that made it stale.
type Entry struct {
	Name string
	Date string // YYYY-MM-DD
}

// ParseDate is parse_date: an ISO date, YYYY-MM-DD or YYYYMMDD, after
// stripping; ok is false for anything else. (Python 3.11 also reads ISO week
// dates; no rota file carries one.)
func ParseDate(s string) (time.Time, bool) {
	s = pystr.Strip(s)
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if len(s) == len(layout) {
			if t, err := time.Parse(layout, s); err == nil && t.Year() >= 1 {
				return t, true
			}
		}
	}
	return time.Time{}, false
}

// gitMtime is the committer date of the last commit that touched rel
// (git_mtime), read in dir; ok is false when git fails, the path is untracked
// or the result is empty.
func gitMtime(dir, rel string) (time.Time, bool) {
	out, ok, err := git.Repo{Dir: dir}.LastCommitDate(context.Background(), rel)
	if err != nil || !ok {
		return time.Time{}, false
	}
	return ParseDate(out)
}

// ErrKind is wrapped by the error for an unknown kind.
var ErrKind = errors.New("unknown kind")

func isStale(d, today time.Time, days int) bool {
	return daysBetween(today, d) >= days
}

// daysBetween is (a - b).days for two dates: floor division, as timedelta.
func daysBetween(a, b time.Time) int {
	h := a.Sub(b).Hours() / 24
	n := int(h)
	if float64(n) > h {
		n--
	}
	return n
}

// Find lists the entries of kind that are days or more old on today. Missing
// files give none. Map entries are dated by their frontmatter "touched" or,
// failing that, the last git commit of the file; the knowledge file, which has
// no per-topic dates, lists every topic with the file's commit date; backlog
// bullets are dated by their Captured field.
func Find(root, kind string, days int, today time.Time) ([]Entry, error) {
	switch kind {
	case "map":
		return findMap(root, days, today)
	case "knowledge":
		return findKnowledge(root, days, today)
	case "todo":
		return findTodo(root, days, today)
	}
	return nil, fmt.Errorf("%w: %s", ErrKind, kind)
}

func fmtDate(t time.Time) string { return t.Format("2006-01-02") }

func findMap(root string, days int, today time.Time) ([]Entry, error) {
	dir := filepath.Join(root, ".rota", "map")
	paths, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	sort.Strings(paths)
	type ent struct {
		name, rel string
		fm        map[string]any
	}
	var ents []ent
	for _, p := range paths {
		text, err := fsio.ReadText(p)
		if err != nil {
			continue
		}
		fm, _, _ := frontmatter.Parse(text)
		name := frontmatter.Str(fm, "subsystem")
		if name == "" {
			continue
		}
		ents = append(ents, ent{name, ".rota/map/" + filepath.Base(p), fm})
	}
	sort.SliceStable(ents, func(i, j int) bool { return ents[i].name < ents[j].name })
	var out []Entry
	for _, e := range ents {
		d, ok := ParseDate(frontmatter.Str(e.fm, "touched"))
		if !ok {
			d, ok = gitMtime(root, e.rel)
		}
		if ok && isStale(d, today, days) {
			out = append(out, Entry{e.name, fmtDate(d)})
		}
	}
	return out, nil
}

func findKnowledge(root string, days int, today time.Time) ([]Entry, error) {
	text, err := fsio.ReadText(filepath.Join(root, ".rota", "KNOWLEDGE.md"))
	if err != nil {
		return nil, nil
	}
	d, ok := gitMtime(root, ".rota/KNOWLEDGE.md")
	if !ok || !isStale(d, today, days) {
		return nil, nil
	}
	var out []Entry
	for _, t := range section.Topics(text) {
		out = append(out, Entry{t.Name, fmtDate(d)})
	}
	return out, nil
}

func findTodo(root string, days int, today time.Time) ([]Entry, error) {
	md, err := (&backlog.File{Root: root}).Markdown(0)
	if errors.Is(err, backlog.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, b := range backlog.OpenBullets(md) {
		captured := b.Fields.Get("captured")
		if captured == "" {
			continue
		}
		if d, ok := ParseDate(captured); ok && isStale(d, today, days) {
			out = append(out, Entry{b.ID, fmtDate(d)})
		}
	}
	return out, nil
}
