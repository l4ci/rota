package stale

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/mapqa"
	"github.com/l4ci/rota/internal/pystr"
)

func date(t *testing.T, s string) time.Time {
	t.Helper()
	d, ok := pystr.ParseDate(s)
	if !ok {
		t.Fatalf("bad date %q", s)
	}
	return d
}

func TestFindReportTodoAgeBoundary(t *testing.T) {
	src := fakeSource{items: []backlog.Item{
		{ID: "old", Fields: backlog.Fields{Captured: "2026-09-02"}},
		{ID: "today", Fields: backlog.Fields{Captured: "2026-10-02"}},
		{ID: "future", Fields: backlog.Fields{Captured: "2026-11-01"}},
	}}
	for _, tc := range []struct {
		days int
		want []string
	}{
		{31, []string{}},
		{30, []string{"old 2026-09-02"}},
		{0, []string{"old 2026-09-02", "today 2026-10-02"}},
		{-30, []string{"old 2026-09-02", "today 2026-10-02", "future 2026-11-01"}},
	} {
		got, err := FindReport(t.TempDir(), "todo", tc.days, date(t, "2026-10-02"), src)
		if err != nil || !reflect.DeepEqual(names(got.Entries), tc.want) || got.SkippedUndated != 0 {
			t.Errorf("days %d: report = %+v, err = %v, want %v", tc.days, got, err, tc.want)
		}
	}
}

// project is a git repo with the given .rota files committed.
func project(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	gittest.Init(t, root, "main")
	os.WriteFile(filepath.Join(root, "x"), []byte("x"), 0o644)
	for p, c := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(c), 0o644)
	}
	gittest.Run(t, root, "add", "-A")
	gittest.Run(t, root, "commit", "-q", "-m", "init")
	return root
}

func names(es []Entry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Name+" "+e.Date)
	}
	return out
}

type fakeSource struct {
	items []backlog.Item
	err   error
}

func (f fakeSource) List(bool) ([]backlog.Item, error) { return f.items, f.err }

// A non-file backend: ids are issue numbers, the root holds no BACKLOG.md.
func TestFindTodoReadsThroughTheSuppliedSource(t *testing.T) {
	item := func(id, captured string) backlog.Item {
		return backlog.Item{ID: id, Fields: backlog.Fields{Captured: captured}}
	}
	src := fakeSource{items: []backlog.Item{
		item("12", "2026-01-01"), item("13", "2026-09-15"), item("14", ""),
		item("15", "not-a-date"), item("16", "20260101"),
	}}
	got, err := FindReport(t.TempDir(), "todo", 90, date(t, "2026-10-02"), src)
	if err != nil || got.SkippedUndated != 2 || !reflect.DeepEqual(names(got.Entries), []string{"12 2026-01-01", "16 2026-01-01"}) {
		t.Errorf("todo = %+v %v", got, err)
	}
	if _, err := FindReport(t.TempDir(), "todo", 90, date(t, "2026-10-02"), fakeSource{err: errors.New("boom")}); err == nil {
		t.Error("source error swallowed")
	}
	if got, err := FindReport(t.TempDir(), "todo", 90, date(t, "2026-10-02"), fakeSource{err: backlog.ErrNotFound}); got.Entries != nil || err != nil {
		t.Errorf("not found = %v %v", got, err)
	}
}

func TestFindKnowledgeUsesTheFileCommitDate(t *testing.T) {
	root := project(t, map[string]string{".rota/KNOWLEDGE.md": "# K\n\n## Alpha\nx\n\n## Beta, gamma\ny\n\n## Delta  \nz\n"})
	far := date(t, "2999-01-01")
	got, err := FindReport(root, "knowledge", 90, far, nil)
	if err != nil || len(got.Entries) != 3 || got.Entries[0].Name != "Alpha" || got.Entries[1].Name != "Beta, gamma" || got.Entries[2].Name != "Delta" {
		t.Fatalf("knowledge = %v %v", names(got.Entries), err)
	}
	if _, ok := pystr.ParseDate(got.Entries[0].Date); !ok {
		t.Errorf("date = %q", got.Entries[0].Date)
	}
	if got, _ := FindReport(root, "knowledge", 90, time.Now(), nil); got.Entries != nil {
		t.Errorf("a file committed today is stale: %v", names(got.Entries))
	}
	if got, _ := FindReport(t.TempDir(), "knowledge", 0, far, nil); got.Entries != nil {
		t.Errorf("missing file: %v", got)
	}
}

func TestFindMap(t *testing.T) {
	fm := func(sub, touched string) string {
		s := "---\nsubsystem: " + sub + "\n"
		if touched != "" {
			s += "touched: " + touched + "\n"
		}
		return s + "---\nbody\n"
	}
	root := project(t, map[string]string{
		".rota/map/auth.md": fm("auth", "2026-01-01"), ".rota/map/cache.md": fm("cache", "2026-09-30"),
		".rota/map/billing.md": fm("billing", ""), ".rota/map/nosub.md": "---\ntitle: x\n---\n", ".rota/map/plain.md": "no frontmatter\n",
		".rota/map/zeta.md": fm("aaa", "2025-12-31"), ".rota/map/bad.md": fm("baddate", "soon"),
	})
	got, err := FindReport(root, "map", 90, date(t, "2026-10-02"), nil)
	if err != nil || got.SkippedUndated != 0 {
		t.Fatalf("map = %+v, err = %v", got, err)
	}
	// Sorted by subsystem; billing and baddate have no touched date, so their git date (today) decides: fresh.
	if !reflect.DeepEqual(names(got.Entries), []string{"aaa 2025-12-31", "auth 2026-01-01"}) {
		t.Errorf("map = %v", names(got.Entries))
	}
	got, _ = FindReport(root, "map", 90, date(t, "2999-01-01"), nil)
	if len(got.Entries) != 5 || got.Entries[0].Name != "aaa" || got.Entries[1].Name != "auth" || got.Entries[2].Name != "baddate" || got.Entries[3].Name != "billing" || got.Entries[4].Name != "cache" {
		t.Errorf("map far future = %v", names(got.Entries))
	}
	if got, _ := FindReport(t.TempDir(), "map", 0, date(t, "2999-01-01"), nil); got.Entries != nil {
		t.Errorf("missing dir: %v", got)
	}
}

func TestFindUntrackedMapFileIsNeverStale(t *testing.T) {
	root := project(t, nil)
	os.MkdirAll(filepath.Join(root, ".rota", "map"), 0o755)
	os.WriteFile(filepath.Join(root, ".rota", "map", "a.md"), []byte("---\nsubsystem: a\n---\n"), 0o644)
	if got, _ := FindReport(root, "map", 0, date(t, "2999-01-01"), nil); got.Entries != nil {
		t.Errorf("untracked file listed: %v", names(got.Entries))
	}
}

func TestFindUnknownKind(t *testing.T) {
	if _, err := FindReport(t.TempDir(), "plans", 1, time.Now(), nil); !errors.Is(err, ErrKind) || !strings.Contains(err.Error(), "plans") {
		t.Errorf("err = %v", err)
	}
}

// Stats (map views) and FindReport date a map file with an unparseable touched alike.
func TestFindMapAgreesWithMapStatsOnUnparseableTouched(t *testing.T) {
	root := project(t, map[string]string{".rota/map/g.md": "---\nsubsystem: g\ntouched: yesterday\n---\nbody\n"})
	st := mapqa.Stats(root)
	got, err := FindReport(root, "map", 0, date(t, "2999-01-01"), nil)
	if err != nil || len(st) != 1 || len(got.Entries) != 1 || st[0].Touched == "" || got.Entries[0].Date != st[0].Touched {
		t.Errorf("FindReport = %v (%v), Stats = %+v", got, err, st)
	}
}
