package backlog

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
		{"archive-flat", ".rota/ARCHIVE.md", "", "- ~~**[B01] [P1] Last.** x~~ Done 2026-01-01 [`abc`]"},
	} {
		for _, ending := range []struct{ name, text string }{{"newline", "\n"}, {"EOF", ""}} {
			t.Run(layout.name+"/"+ending.name, func(t *testing.T) {
				keep := "# Items\n\n"
				if layout.section != "" {
					keep += "## " + layout.section + "\n"
				}
				keep += "- **[B02] [P2] Keep.** y\n"
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

func TestRemoveWriterArchive(t *testing.T) {
	for _, layout := range []struct{ name, header, section string }{
		{"flat", "", "Archive"},
		{"sectioned", "# Archive\n\n## Completed\n", "Completed"},
		{"dated", "# Archive\n\n## 2025\n", "2025"},
	} {
		for _, scrub := range []bool{false, true} {
			name := layout.name + "/keep"
			if scrub {
				name = layout.name + "/scrub"
			}
			t.Run(name, func(t *testing.T) {
				target := "- ~~**[B01] Remove.** body~~ Done 2025-01-01 [`abc`]\n"
				onlyRef := "- ~~**[B02] Keep.** Related: [B01]~~ Done 2025-01-01 [`def`] (blocked: see [B01])\n"
				multiRef := "- ~~**[B03] Keep too.** Related: [B01], [F01]~~ Done 2025-01-01 [`ghi`]\n"
				unrelated := "- ~~**[B04] Unrelated.** body~~ Done 2025-01-01 [`jkl`]\n"
				files := map[string]string{
					".rota/BACKLOG.md":  "# Backlog\n\n## Bugs\n- **[B05] Open.** Related: [B01], [F01]\n\n## Completed\n" + target + onlyRef + multiRef + unrelated,
					".rota/bugs/B01.md": "detail", ".rota/plans/M01-B01.md": "plan",
				}
				if layout.header != "" {
					files[".rota/ARCHIVE.md"] = layout.header
				}
				f, _ := proj(t, files)
				if n, err := f.Archive(5, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil || n != 4 {
					t.Fatalf("archive: moved=%d err=%v", n, err)
				}
				archiveBefore := read(t, f, ".rota/ARCHIVE.md")
				backlogBefore := read(t, f, ".rota/BACKLOG.md")
				wantRefs := 1
				if scrub {
					wantRefs = 3
				}
				for _, apply := range []bool{false, true} {
					res, err := f.Remove([]string{"B01"}, scrub, apply)
					if err != nil || res.Applied != apply || len(res.Items) != 1 {
						t.Fatalf("remove apply=%t: res=%+v err=%v", apply, res, err)
					}
					it := res.Items[0]
					if !it.Archive || it.TodoEntry || it.Section != layout.section || it.CrossRefs != wantRefs {
						t.Errorf("item=%+v; want archive in %s and %d references", it, layout.section, wantRefs)
					}
					if !apply {
						if read(t, f, ".rota/ARCHIVE.md") != archiveBefore || read(t, f, ".rota/BACKLOG.md") != backlogBefore {
							t.Fatal("preview changed a document")
						}
						for _, path := range []string{".rota/bugs/B01.md", ".rota/plans/M01-B01.md"} {
							if _, err := os.Stat(filepath.Join(f.Root, path)); err != nil {
								t.Fatalf("preview removed %s: %v", path, err)
							}
						}
					}
				}
				wantArchive := archiveBefore
				if scrub {
					wantArchive = strings.Replace(wantArchive, target, "", 1)
					wantArchive = strings.Replace(wantArchive, " Related: [B01]~~", "~~", 1)
					wantArchive = strings.Replace(wantArchive, "Related: [B01], [F01]", "Related: [F01]", 1)
				}
				if got := read(t, f, ".rota/ARCHIVE.md"); got != wantArchive {
					t.Errorf("archive after removal:\n%s\nwant:\n%s", got, wantArchive)
				}
				if got := read(t, f, ".rota/BACKLOG.md"); got != strings.Replace(backlogBefore, "Related: [B01], [F01]", "Related: [F01]", 1) {
					t.Errorf("backlog after removal:\n%s", got)
				}
				for _, path := range []string{".rota/bugs/B01.md", ".rota/plans/M01-B01.md"} {
					if _, err := os.Stat(filepath.Join(f.Root, path)); !os.IsNotExist(err) {
						t.Errorf("artifact remains: %s (%v)", path, err)
					}
				}
			})
		}
	}
}

func TestRemoveArchiveRemainingAborts(t *testing.T) {
	before := "# Archive\n\n- ~~**[B01] First copy.**~~ Done 2025-01-01 [`abc`]\n\n## Completed\n- ~~**[B01] Second copy.**~~ Done 2025-01-01 [`def`]"
	f := rmProject(t, map[string]string{
		".rota/BACKLOG.md": "# Backlog\n",
		".rota/ARCHIVE.md": before,
	})
	for _, apply := range []bool{false, true} {
		res, err := f.Remove([]string{"B01"}, true, apply)
		if !errors.Is(err, ErrInvalid) || res.Applied {
			t.Fatalf("apply=%t: res=%+v err=%v; want duplicate refusal", apply, res, err)
		}
		if read(t, f, ".rota/ARCHIVE.md") != before {
			t.Fatal("failed removal changed the archive")
		}
		for _, path := range []string{".rota/bugs/B01.md", ".rota/plans/M01-B01.md"} {
			if _, err := os.Stat(filepath.Join(f.Root, path)); err != nil {
				t.Fatalf("failed removal deleted %s: %v", path, err)
			}
		}
	}
}
