package backlog

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReady(t *testing.T) {
	both := []string{"no acceptance criteria in the issue body", "no design or plan note"}
	cases := []struct {
		name  string
		files map[string]string
		id    string
		want  []string
		err   error
	}{
		{"nothing", nil, "B02", both, nil},
		{"acceptance heading", map[string]string{".rota/bugs/B01.md": "# x\n### Acceptance criteria\n"}, "B01", []string{}, nil},
		{"lowercase heading", map[string]string{".rota/bugs/B01.md": "## my acceptance\n"}, "B01", []string{}, nil},
		{"checkbox", map[string]string{".rota/bugs/B01.md": "  * [X] done\n"}, "B01", []string{}, nil},
		{"word without heading", map[string]string{".rota/bugs/B01.md": "acceptance\n"}, "B01", both, nil},
		{"design note", map[string]string{".rota/designs/B02.md": "d"}, "B02", []string{}, nil},
		{"plan note", map[string]string{".rota/plans/M01-B02.md": "p"}, "B02", []string{}, nil},
		{"plan of another item", map[string]string{".rota/plans/M01-B20.md": "p"}, "B02", both, nil},
		{"unknown", nil, "B99", nil, ErrNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, _ := proj(t, rotaFiles(c.files))
			got, err := f.Ready(c.id)
			if !errors.Is(err, c.err) || (err == nil && !reflect.DeepEqual(got, c.want)) {
				t.Fatalf("got %v, %v; want %v, %v", got, err, c.want, c.err)
			}
		})
	}
}

const log = `# B01

## Log
- 2026-09-01 · question · Why?
  more

  tail
- 2026-09-02 · answer · Because.
- not a row
- 2026-09-03 · decision · Ship.

## Notes
- 2026-09-04 · question · outside the log
`

func TestComments(t *testing.T) {
	f, _ := proj(t, rotaFiles(map[string]string{".rota/bugs/B01.md": log}))
	all, err := f.Comments("B01", "")
	if err != nil {
		t.Fatal(err)
	}
	want := []Comment{
		{Who: "2026-09-01", Kind: "question", Text: "Why?\nmore\n\ntail"},
		{Who: "2026-09-02", Kind: "answer", Text: "Because."},
		{Who: "2026-09-03", Kind: "decision", Text: "Ship."},
	}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("all = %#v", all)
	}
	if got, _ := f.Comments("B01", "answer"); len(got) != 1 || got[0].Text != "Because." {
		t.Errorf("filtered = %#v", got)
	}
	if got, err := f.Comments("B02", ""); err != nil || got == nil || len(got) != 0 {
		t.Errorf("no log = %#v, %v", got, err)
	}
	if _, err := f.Comments("B01", "remark"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad kind: %v", err)
	}
	if _, err := f.Comments("B99", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
}

func TestAddComment(t *testing.T) {
	t.Run("existing log", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(map[string]string{".rota/bugs/B01.md": log}))
		if id, err := f.AddComment("B01", "feedback", "line one\r\n\r\n  indented\n"); err != nil || id != "" {
			t.Fatalf("id=%q err=%v", id, err)
		}
		got := read(t, f, ".rota/bugs/B01.md")
		if !strings.Contains(got, " · feedback · line one\n\n    indented\n## Notes") {
			t.Errorf("detail:\n%s", got)
		}
		rows, _ := f.Comments("B01", "feedback")
		if len(rows) != 1 || rows[0].Text != "line one\n\n  indented" {
			t.Errorf("round trip = %#v", rows)
		}
	})
	t.Run("creates the detail file", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		if _, err := f.AddComment("B02", "question", "Is it?"); err != nil {
			t.Fatal(err)
		}
		got := read(t, f, ".rota/bugs/B02.md")
		if !strings.HasPrefix(got, "# B02: Second\n\n> Related TODO entry: `[B02]` in `.rota/BACKLOG.md`\n\n## Log\n\n- ") {
			t.Errorf("detail:\n%s", got)
		}
	})
	t.Run("errors", func(t *testing.T) {
		f, _ := proj(t, rotaFiles(nil))
		if _, err := f.AddComment("B02", "remark", "x"); !errors.Is(err, ErrInvalid) {
			t.Errorf("kind: %v", err)
		}
		if _, err := f.AddComment("B99", "answer", "x"); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown: %v", err)
		}
	})
}

// A pinned clock stamps the comment row, whatever the real day is, so a
// frozen run that crosses local midnight still records one date.
func TestAddCommentStampsInjectedToday(t *testing.T) {
	f, _ := proj(t, rotaFiles(nil))
	pinned := time.Date(2001, 2, 3, 23, 59, 59, 0, time.UTC)
	f.Today = func() time.Time { return pinned }
	for range 2 {
		if _, err := f.AddComment("B02", "question", "Is it?"); err != nil {
			t.Fatal(err)
		}
		pinned = pinned.Add(2 * time.Second) // crosses midnight; Today is read per call
	}
	got := read(t, f, ".rota/bugs/B02.md")
	if !strings.Contains(got, "- 2001-02-03 · question") || !strings.Contains(got, "- 2001-02-04 · question") {
		t.Errorf("rows not stamped from the injected clock:\n%s", got)
	}
}
