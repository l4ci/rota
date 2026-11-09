// Package stale lists stale entries of the backlog, the knowledge file and the
// subsystem map, as hv-staleness does.
package stale

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitmap"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/mapqa"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/section"
	"github.com/l4ci/rota/internal/stamp"
)

// Kinds are the values of --kind.
var Kinds = []string{"map", "knowledge", "todo"}

// Entry is one stale entry and the date that made it stale.
type Entry struct {
	Name string
	Date string // YYYY-MM-DD
}

// gitMtime is the committer date of the last commit that touched rel
// (git_mtime), read in dir; ok is false when git fails, the path is untracked
// or the result is empty.
func gitMtime(dir, rel string) (time.Time, bool) {
	out, ok, err := git.Repo{Dir: dir}.LastCommitDate(context.Background(), rel)
	if err != nil || !ok {
		return time.Time{}, false
	}
	return pystr.ParseDate(out)
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

// TodoSource lists the open backlog items of whichever backend the project
// selected; the caller supplies it, so stale never imports cli. backlog.Backend
// satisfies it.
type TodoSource interface {
	List(includeClosed bool) ([]backlog.Item, error)
}

// Find lists the entries of kind that are days or more old on today. Missing
// files give none. Map entries are dated by their frontmatter "touched" or,
// failing that, the last git commit of the file; the knowledge file, which has
// no per-topic dates, lists every topic with the file's commit date; open
// backlog items from todos (the caller's selected backend; nil lists none) are
// dated by their Captured field, and an item without one is skipped.
func Find(root, kind string, days int, today time.Time, todos TodoSource) ([]Entry, error) {
	switch kind {
	case "map":
		return findMap(root, days, today)
	case "knowledge":
		return findKnowledge(root, days, today)
	case "todo":
		return findTodo(days, today, todos)
	}
	return nil, fmt.Errorf("%w: %s", ErrKind, kind)
}

func fmtDate(t time.Time) string { return stamp.Date(t) }

func findMap(root string, days int, today time.Time) ([]Entry, error) {
	var out []Entry
	for _, e := range mapqa.Entries(root) {
		if d, ok := e.Touched(root); ok && isStale(d, today, days) {
			out = append(out, Entry{e.Name, fmtDate(d)})
		}
	}
	return out, nil
}

func findKnowledge(root string, days int, today time.Time) ([]Entry, error) {
	text, err := fsio.ReadText(rotatree.File(root, rotatree.KnowledgeFile))
	if err != nil {
		return nil, nil
	}
	d, ok := gitMtime(root, rotatree.Rel(rotatree.KnowledgeFile))
	if !ok || !isStale(d, today, days) {
		return nil, nil
	}
	var out []Entry
	for _, t := range section.Topics(text) {
		out = append(out, Entry{t.Name, fmtDate(d)})
	}
	return out, nil
}

func findTodo(days int, today time.Time, todos TodoSource) ([]Entry, error) {
	if todos == nil {
		return nil, nil
	}
	items, err := todos.List(false)
	if exitmap.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, it := range items {
		captured := it.Fields.Captured
		if captured == "" {
			continue
		}
		if d, ok := pystr.ParseDate(captured); ok && isStale(d, today, days) {
			out = append(out, Entry{it.ID, fmtDate(d)})
		}
	}
	return out, nil
}
