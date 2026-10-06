package backlog

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRemoveCrossRefs(t *testing.T) {
	cases := []struct{ name, line, id, want string }{
		{"only ref", "- **[B02] x.** y Related: [B01] Since: a", "B01", "- **[B02] x.** y Since: a"},
		{"keeps the rest", "- **[B02] x.** y Related: [B01], [F01], [T01] Since: a", "B01", "- **[B02] x.** y Related: [F01], [T01] Since: a"},
		{"no space after comma", "- **[B02] x.** Related: [B01],[F01]", "B01", "- **[B02] x.** Related: [F01]"},
		{"related last", "- **[B02] x.** y Related: [B01]", "B01", "- **[B02] x.** y"},
		{"not related", "- **[B02] x.** y Related: [F01]", "B01", "- **[B02] x.** y Related: [F01]"},
		{"no space after colon is not stripped", "- **[B02] x.** y Related:[B01]", "B01", "- **[B02] x.** y Related:[B01]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := removeIDFromRelated(c.line, c.id); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestStripBullet(t *testing.T) {
	cases := []struct{ name, in, bullet, want string }{
		{"drops line and one blank", "a\n- x\n\nb\n", "- x", "a\n\nb\n"},
		{"no blank after", "a\n- x\nb\n", "- x", "a\nb\n"},
		{"last line without newline", "a\n- x", "- x", "a\n"},
		{"prefix of a longer line stays", "- xy\n", "- x", "- xy\n"},
		{"prefix at EOF stays", "- xy", "- x", "- xy"},
	}
	for _, c := range cases {
		if got := stripBullet(c.in, c.bullet); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestRemoveFinalBullet(t *testing.T) {
	for _, layout := range []struct{ name, path, section, bullet string }{
		{"backlog-open", ".rota/BACKLOG.md", "Bugs", "- **[B01] [P1] Last.** x"},
		{"backlog-completed", ".rota/BACKLOG.md", "Completed", "- ~~**[B01] [P1] Last.** x~~ Done 2026-01-01 [`abc`]"},
		{"archive-open", ".rota/ARCHIVE.md", "Bugs", "- **[B01] [P1] Last.** x"},
		{"archive-completed", ".rota/ARCHIVE.md", "Completed", "- ~~**[B01] [P1] Last.** x~~ Done 2026-01-01 [`abc`]"},
	} {
		for _, ending := range []struct{ name, text string }{{"newline", "\n"}, {"EOF", ""}} {
			t.Run(layout.name+"/"+ending.name, func(t *testing.T) {
				keep := "# Items\n\n## " + layout.section + "\n- **[B02] [P2] Keep.** y\n"
				f := rmProject(t, map[string]string{
					".rota/BACKLOG.md": "# Backlog\n",
					layout.path:        keep + layout.bullet + ending.text,
				})
				before := read(t, f, layout.path)
				res, err := f.Remove([]string{"B01"}, true, false)
				if err != nil || res.Applied || len(res.Items) != 1 {
					t.Fatalf("preview: res=%+v err=%v", res, err)
				}
				if read(t, f, layout.path) != before {
					t.Fatal("preview changed the document")
				}
				for _, artifact := range []string{".rota/bugs/B01.md", ".rota/plans/M01-B01.md"} {
					if _, err := os.Stat(filepath.Join(f.Root, artifact)); err != nil {
						t.Fatalf("preview removed %s: %v", artifact, err)
					}
				}
				res, err = f.Remove([]string{"B01"}, true, true)
				if err != nil || !res.Applied || len(res.Items) != 1 {
					t.Fatalf("apply: res=%+v err=%v", res, err)
				}
				if got := read(t, f, layout.path); got != keep {
					t.Errorf("document after removal = %q; want %q", got, keep)
				}
				for _, artifact := range []string{".rota/bugs/B01.md", ".rota/plans/M01-B01.md"} {
					if _, err := os.Stat(filepath.Join(f.Root, artifact)); !os.IsNotExist(err) {
						t.Errorf("%s not removed: %v", artifact, err)
					}
				}
				if got := read(t, f, ".rota/plans/M01-B10.md"); got != "other" {
					t.Errorf("unrelated plan changed: %q", got)
				}
			})
		}
	}
}

func rmProject(t *testing.T, extra map[string]string) *File {
	f, _ := proj(t, rotaFiles(map[string]string{
		".rota/bugs/B01.md":      "d",
		".rota/plans/M01-B01.md": "p",
		".rota/plans/M01-B10.md": "other",
		".rota/ARCHIVE.md":       "# Archive\n\n## Completed\n- ~~**[B05] x.** y Related: [B01]~~ Done d [`a`]\n",
	}))
	for k, v := range extra {
		os.WriteFile(filepath.Join(f.Root, k), []byte(v), 0o644)
	}
	return f
}

func TestRemove(t *testing.T) {
	t.Run("item remaining aborts before cleanup", func(t *testing.T) {
		before := "# Backlog\n\n## Bugs\n- **[B01] First copy.** x\n- **[B01] Second copy.** y"
		f := rmProject(t, map[string]string{".rota/BACKLOG.md": before})
		res, err := f.Remove([]string{"B01"}, true, true)
		if err == nil || res.Applied {
			t.Errorf("res=%+v err=%v; want failed removal", res, err)
		}
		if read(t, f, ".rota/BACKLOG.md") != before {
			t.Error("failed removal changed the backlog")
		}
		for _, artifact := range []string{".rota/bugs/B01.md", ".rota/plans/M01-B01.md"} {
			if _, err := os.Stat(filepath.Join(f.Root, artifact)); err != nil {
				t.Errorf("failed removal deleted %s: %v", artifact, err)
			}
		}
	})
	t.Run("preview writes nothing", func(t *testing.T) {
		f := rmProject(t, nil)
		before := read(t, f, ".rota/BACKLOG.md")
		res, err := f.Remove([]string{"B01"}, true, false)
		if err != nil || res.Applied {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		it := res.Items[0]
		if it.CrossRefs != 3 || it.DetailFile != ".rota/bugs/B01.md" || !reflect.DeepEqual(it.PlanFiles, []string{".rota/plans/M01-B01.md"}) || !it.TodoEntry || it.Type != "B" {
			t.Errorf("item = %+v", it)
		}
		if read(t, f, ".rota/BACKLOG.md") != before {
			t.Error("preview wrote")
		}
		if _, err := os.Stat(filepath.Join(f.Root, ".rota/bugs/B01.md")); err != nil {
			t.Error("preview deleted the detail file")
		}
	})
	t.Run("apply", func(t *testing.T) {
		f := rmProject(t, nil)
		res, err := f.Remove([]string{"B01"}, true, true)
		if err != nil || !res.Applied {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		bl := read(t, f, ".rota/BACKLOG.md")
		if strings.Contains(bl, "[B01]") {
			t.Errorf("a reference survived:\n%s", bl)
		}
		if !strings.Contains(bl, "- **[B02] [P2] Second.** y Related: [F01], [T01]\n") {
			t.Errorf("B02:\n%s", bl)
		}
		if strings.Contains(read(t, f, ".rota/ARCHIVE.md"), "Related") {
			t.Error("archive reference survived --scrub-archive")
		}
		for _, gone := range []string{".rota/bugs/B01.md", ".rota/plans/M01-B01.md"} {
			if _, err := os.Stat(filepath.Join(f.Root, gone)); !os.IsNotExist(err) {
				t.Errorf("%s not removed", gone)
			}
		}
		if _, err := os.Stat(filepath.Join(f.Root, ".rota/plans/M01-B10.md")); err != nil {
			t.Error("another item's plan was removed")
		}
	})
	t.Run("archived item", func(t *testing.T) {
		f := rmProject(t, nil)
		res, err := f.Remove([]string{"B05"}, false, true)
		if err != nil || !res.Items[0].Archive || res.Items[0].TodoEntry {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		if !strings.Contains(read(t, f, ".rota/ARCHIVE.md"), "[B05]") {
			t.Error("the archive entry goes only with scrub")
		}
	})
	t.Run("unknown ID aborts before any write", func(t *testing.T) {
		f := rmProject(t, nil)
		before := read(t, f, ".rota/BACKLOG.md")
		if _, err := f.Remove([]string{"B01", "B99"}, false, true); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
		if read(t, f, ".rota/BACKLOG.md") != before {
			t.Error("wrote")
		}
	})
	t.Run("active branch", func(t *testing.T) {
		f := rmProject(t, map[string]string{".rota/status.json": `{"active": [{"branch": "feat/x", "items": "T01, B01"}]}`})
		res, err := f.Remove([]string{"B01"}, false, false)
		if err != nil || res.Items[0].ActiveBranch != "feat/x" {
			t.Fatalf("preview: %+v %v", res, err)
		}
		_, err = f.Remove([]string{"B01"}, false, true)
		var a *ActiveError
		if !errors.As(err, &a) || a.ID != "B01" || a.Branch != "feat/x" {
			t.Fatalf("apply err = %v", err)
		}
		if _, e := os.Stat(filepath.Join(f.Root, ".rota/bugs/B01.md")); e != nil {
			t.Error("a refused removal deleted a file")
		}
	})
}
