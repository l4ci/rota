// Package stale lists stale entries of the backlog, the knowledge file and the
// subsystem map; ported from the earlier hv-staleness helper.
package stale

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitmap"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/mapqa"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/rotatree"
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
// field. SkippedUndated counts items without a usable
// Captured date (always 0 for the map and knowledge kinds).
func FindReport(root, kind string, days int, today time.Time, todos TodoSource) (Report, error) {
	var es []Entry
	var skipped int
	var err error
	repo := git.Repo{Dir: root}
	switch kind {
	case "map":
		es = findMap(repo, root, days, today)
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

func findMap(repo git.Repo, root string, days int, today time.Time) []Entry {
	var out []Entry
	for _, e := range mapqa.Entries(root) {
		if d, ok := e.TouchedIn(repo, root); ok && isStale(d, today, days) {
			out = append(out, Entry{e.Name, fmtDate(d)})
		}
	}
	return out
}

func findKnowledge(repo git.Repo, root string, days int, today time.Time) ([]Entry, error) {
	text, err := fsio.ReadText(rotatree.File(root, rotatree.KnowledgeFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	dates, ok := lineDates(repo, rotatree.Rel(rotatree.KnowledgeFile))
	if !ok {
		return nil, nil
	}
	var out []Entry
	for _, t := range topicSpans(text) {
		d, ok := latestDate(dates, t.from, t.to)
		if !ok || !isStale(d, today, days) {
			continue
		}
		out = append(out, Entry{t.name, fmtDate(d)})
	}
	return out, nil
}

// topicSpan is one "## " topic of a file: its name and the 0-based lines
// [from, to) it spans, heading included. The heading rule is section.Topics'.
type topicSpan struct {
	name     string
	from, to int
}

func topicSpans(text string) []topicSpan {
	lines := strings.Split(text, "\n")
	var out []topicSpan
	for i, l := range lines {
		if len(l) > 3 && strings.HasPrefix(l, "## ") {
			if n := len(out); n > 0 {
				out[n-1].to = i
			}
			out = append(out, topicSpan{name: pystr.Strip(l[3:]), from: i, to: len(lines)})
		}
	}
	return out
}

// lineDates is the day of the commit that last changed each line of the file
// at rel in repo's working copy (index i is line i+1), from git blame. ok is
// false when git cannot blame the file, for example when it is untracked.
func lineDates(repo git.Repo, rel string) ([]time.Time, bool) {
	out, ok, err := repo.Output(context.Background(), "blame", "--porcelain", "--", rel)
	if err != nil || !ok {
		return nil, false
	}
	var lineSha []string
	epoch := map[string]int64{}
	zone := map[string]string{}
	var cur string
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		switch {
		case strings.HasPrefix(l, "\t"):
			// file content: no metadata
		case strings.HasPrefix(l, "committer-time "):
			epoch[cur], _ = strconv.ParseInt(strings.TrimPrefix(l, "committer-time "), 10, 64)
		case strings.HasPrefix(l, "committer-tz "):
			zone[cur] = strings.TrimPrefix(l, "committer-tz ")
		case len(f) >= 3 && len(f[0]) == 40:
			n, err := strconv.Atoi(f[2])
			if err != nil || n < 1 {
				continue
			}
			cur = f[0]
			for len(lineSha) < n {
				lineSha = append(lineSha, "")
			}
			lineSha[n-1] = cur
		}
	}
	dates := make([]time.Time, len(lineSha))
	for i, sha := range lineSha {
		if sha != "" {
			dates[i] = localDay(epoch[sha], zone[sha])
		}
	}
	return dates, true
}

// localDay is the calendar day of an epoch time in the committer's own zone
// (+hhmm), as git log %cs reports it, at midnight UTC.
func localDay(secs int64, zone string) time.Time {
	off := int64(0)
	if len(zone) == 5 && (zone[0] == '+' || zone[0] == '-') {
		h, errH := strconv.Atoi(zone[1:3])
		m, errM := strconv.Atoi(zone[3:5])
		if errH == nil && errM == nil {
			off = int64(h*3600 + m*60)
			if zone[0] == '-' {
				off = -off
			}
		}
	}
	t := time.Unix(secs+off, 0).UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
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
