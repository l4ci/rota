// Package stale lists stale entries of the backlog, the knowledge file and the
// subsystem map; ported from the earlier hv-staleness helper.
package stale

import (
	"context"
	"errors"
	"fmt"
	"os"
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

// Report lists stale entries and how many open todo items could not be dated.
type Report struct {
	Entries        []Entry
	SkippedUndated int
}

// FindReport lists the entries of kind that are days or more old on today.
// Missing files give none. Map entries are dated by their frontmatter "touched"
// or, failing that, the last git commit of the file. Each knowledge topic is
// dated by the newest commit that changed one of its own lines (git blame), so
// a recently edited sibling does not hide it. Open backlog items from todos
// (the caller's selected backend; nil lists none) are dated by their Captured
// field. A knowledge file git cannot blame (untracked) gives none; a blame that
// fails to run is an error. SkippedUndated counts items without a usable
// Captured date (always 0 for the map and knowledge kinds).
func FindReport(repo git.Repo, root, kind string, days int, today time.Time, todos TodoSource) (Report, error) {
	var es []Entry
	var skipped int
	var err error
	switch kind {
	case "map":
		es, err = findMap(repo, root, days, today)
	case "knowledge":
		es, err = findKnowledge(repo, root, days, today)
	case "todo":
		es, skipped, err = findTodo(days, today, todos)
	default:
		err = fmt.Errorf("%w: %s", ErrKind, kind)
	}
	return Report{Entries: es, SkippedUndated: skipped}, err
}

func fmtDate(t time.Time) string { return stamp.Date(t) }

func findMap(repo git.Repo, root string, days int, today time.Time) ([]Entry, error) {
	entries, err := mapqa.Entries(root)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range entries {
		if d, ok := e.TouchedIn(repo, root); ok && isStale(d, today, days) {
			out = append(out, Entry{e.Name, fmtDate(d)})
		}
	}
	return out, nil
}

func findKnowledge(repo git.Repo, root string, days int, today time.Time) ([]Entry, error) {
	text, err := fsio.ReadUserText(rotatree.File(root, rotatree.KnowledgeFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dates, ok, err := repo.BlameLineDates(context.Background(), rotatree.Rel(rotatree.KnowledgeFile))
	if err != nil {
		return nil, fmt.Errorf("blame %s: %w", rotatree.Rel(rotatree.KnowledgeFile), err)
	}
	if !ok {
		return nil, nil
	}
	var out []Entry
	for _, t := range section.TopicSpans(text) {
		d, ok := latestDate(dates, t.From, t.To)
		if !ok || !isStale(d, today, days) {
			continue
		}
		out = append(out, Entry{t.Name, fmtDate(d)})
	}
	return out, nil
}

// latestDate is the newest date among dates[from:to] (clamped to the slice).
func latestDate(dates []time.Time, from, to int) (time.Time, bool) {
	var best time.Time
	for i := from; i < to && i < len(dates); i++ {
		if dates[i].After(best) {
			best = dates[i]
		}
	}
	return best, !best.IsZero()
}

func findTodo(days int, today time.Time, todos TodoSource) ([]Entry, int, error) {
	if todos == nil {
		return nil, 0, nil
	}
	items, err := todos.List(false)
	if exitmap.IsNotFound(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var out []Entry
	skipped := 0
	for _, it := range items {
		d, ok := pystr.ParseDate(it.Fields.Captured)
		if !ok {
			skipped++
			continue
		}
		if isStale(d, today, days) {
			out = append(out, Entry{it.ID, fmtDate(d)})
		}
	}
	return out, skipped, nil
}
