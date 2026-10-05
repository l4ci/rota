package milestone

import (
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func exitOf(err error) int {
	var ae *exitcode.Error
	if errors.As(err, &ae) {
		return ae.Exit
	}
	return 0
}

var created = regexp.MustCompile(`(?m)^created: \d{4}-\d{2}-\d{2}$`)

func golden(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join("testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func read(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// project copies testdata/fixture (a seeded MILESTONES.md and CLAUDE.md).
func project(t *testing.T) string {
	root := t.TempDir()
	fix := filepath.Join("testdata", "fixture")
	err := filepath.Walk(fix, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fix, p)
		if fi.IsDir() {
			return os.MkdirAll(filepath.Join(root, rel), 0o777)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func snap(t *testing.T, root, stage string) {
	t.Helper()
	for _, f := range []struct{ file, name string }{{"CLAUDE.md", "CLAUDE.md"}, {".rota/MILESTONES.md", "MILESTONES.md"}} {
		if got := read(t, filepath.Join(root, f.file)); got != golden(t, stage+"."+f.name) {
			t.Errorf("%s %s differs from golden:\n%s", stage, f.name, got)
		}
	}
}

// The scenario is the one the goldens were recorded on; every file must stay
// byte-identical to what the retired hv-vision-* helpers wrote. The goldens are
// frozen and change only by reviewed edit.
func TestMatchesOldHelpers(t *testing.T) {
	root := project(t)
	if changed, err := Index(root); err != nil || !changed {
		t.Fatalf("index: %v %v", changed, err)
	}
	snap(t, root, "s0")
	for i, a := range []struct{ title, summary, deps, id string }{
		{"First: thing & é", "Summary line.", "", "M01"},
		{"Second", "Needs the first.", "M01", "M02"},
		{"Third", "Needs both.", "M01, M02", "M03"},
	} {
		id, err := Add(root, a.title, a.summary, a.deps)
		if err != nil || id != a.id {
			t.Fatalf("add %d: %q %v", i, id, err)
		}
	}
	Index(root)
	snap(t, root, "s1")
	for _, st := range []struct{ id, to string }{{"M01", "shipped"}, {"M02", "active"}, {"M03", "active"}} {
		if _, err := SetStatus(root, st.id, st.to); err != nil {
			t.Fatal(err)
		}
	}
	snap(t, root, "s2")
	for _, id := range []string{"M01", "M02", "M03"} {
		if got := created.ReplaceAllString(read(t, filepath.Join(root, ".rota/milestones", id+".md")), "created: DATE"); got != golden(t, id+".md") {
			t.Errorf("%s differs from golden:\n%s", id, got)
		}
	}
	list, err := List(root)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: %v %v", list, err)
	}
	want := []Entry{{"M01", "First: thing & é", "shipped", []string{}, true}, {"M02", "Second", "active", []string{"M01"}, true}, {"M03", "Third", "active", []string{"M01", "M02"}, false}}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("list = %+v", list)
	}
	if ids, _ := Active(root); !reflect.DeepEqual(ids, strings.Fields(golden(t, "active.txt"))) {
		t.Errorf("active = %v", ids)
	}
	SetStatus(root, "M02", "shipped")
	SetStatus(root, "M03", "archived")
	snap(t, root, "s3")
}

func TestStatusAndIndexChanged(t *testing.T) {
	root := project(t)
	Add(root, "A", "a", "")
	if changed, err := SetStatus(root, "M01", "active"); err != nil || !changed {
		t.Fatalf("first status: %v %v", changed, err)
	}
	if changed, err := SetStatus(root, "M01", "active"); err != nil || changed {
		t.Errorf("repeat status: changed=%v %v", changed, err)
	}
	if changed, _ := Index(root); changed {
		t.Error("index of an up-to-date tree reported changed")
	}
	// Drift in MILESTONES.md heals.
	p := filepath.Join(root, ".rota/MILESTONES.md")
	os.WriteFile(p, []byte(strings.Replace(read(t, p), "**Status:** active", "**Status:** planned", 1)), 0o644)
	if changed, _ := Index(root); !changed {
		t.Error("index did not report healing drift")
	}
	if !strings.Contains(read(t, p), "**Status:** active") {
		t.Error("status line not healed")
	}
}

func TestExits(t *testing.T) {
	root := project(t)
	Add(root, "A", "a", "")
	for _, id := range []string{"m01", "M1", "X01", "../x"} {
		if _, err := Show(root, id); exitOf(err) != 2 {
			t.Errorf("Show(%q) = %v", id, err)
		}
		if _, err := SetStatus(root, id, "active"); exitOf(err) != 2 {
			t.Errorf("SetStatus(%q) = %v", id, err)
		}
	}
	if _, err := SetStatus(root, "M01", "done"); exitOf(err) != 2 {
		t.Errorf("bad status: %v", err)
	}
	if _, err := Show(root, "M09"); exitOf(err) != 3 {
		t.Errorf("show missing: %v", err)
	}
	if _, err := SetStatus(root, "M09", "active"); exitOf(err) != 3 {
		t.Errorf("status missing: %v", err)
	}
	if _, err := Put(root, "M09", "---\nid: M09\n---\n"); exitOf(err) != 3 {
		t.Errorf("put missing: %v", err)
	}
	for _, text := range []string{"no frontmatter", "---\nid: M02\n---\n", "---\ntitle: x\n---\n"} {
		if _, err := Put(root, "M01", text); exitOf(err) != 4 {
			t.Errorf("Put(%q) = %v, want exit 4", text, err)
		}
	}
	if changed, err := Put(root, "M01", "---\r\nid: M01\r\nstatus: planned\r\n---\r\nbody\r\n"); err != nil || !changed {
		t.Fatalf("put crlf: %v %v", changed, err)
	}
	if changed, _ := Put(root, "M01", "---\r\nid: M01\r\nstatus: planned\r\n---\r\nbody\r\n"); changed {
		t.Error("identical put reported changed")
	}
	// Status on a file without a status field is a bug in the file: exit 70.
	os.WriteFile(filepath.Join(root, ".rota/milestones/M01.md"), []byte("---\nid: M01\n---\n"), 0o644)
	if _, err := SetStatus(root, "M01", "active"); exitOf(err) != 70 {
		t.Errorf("no status field: %v", err)
	}
}

func TestListStringDependsAndDefaults(t *testing.T) {
	root := project(t)
	dir := filepath.Join(root, ".rota/milestones")
	os.MkdirAll(dir, 0o777)
	os.WriteFile(filepath.Join(dir, "M05.md"), []byte("---\ntitle: T\ndepends: M01, M03\n---\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "M06.md"), []byte("# no frontmatter\n"), 0o644)
	list, _ := List(root)
	if len(list) != 1 || list[0].ID != "M05" || list[0].Status != "planned" || !reflect.DeepEqual(list[0].Depends, []string{"M01", "M03"}) || list[0].Ready {
		t.Fatalf("list = %+v", list)
	}
}

func TestAddWithoutOverviewAndConcurrent(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o777)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[string]bool{}
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := Add(root, "T", "s", "")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			ids[id] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(ids) != 6 {
		t.Fatalf("ids = %v", ids)
	}
	if n := strings.Count(read(t, filepath.Join(root, ".rota/MILESTONES.md")), "### M0"); n != 6 {
		t.Errorf("%d overview entries, want 6", n)
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	root := t.TempDir()
	var st Store = FileStore{Root: root}
	if st.OnTracker() {
		t.Fatal("file store reports OnTracker")
	}
	id, err := st.Add("Title", "Summary.", []string{"M09"})
	if err != nil || id != "M01" {
		t.Fatalf("add = %q %v", id, err)
	}
	if changed, err := st.SetStatus(id, "active"); err != nil || !changed {
		t.Fatalf("set status = %v %v", changed, err)
	}
	list, err := st.List()
	if err != nil || len(list) != 1 || !reflect.DeepEqual(ActiveIDs(list), []string{id}) || list[0].Ready {
		t.Fatalf("list = %+v %v", list, err)
	}
	text, err := st.Show(id)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := st.Put(id, text); err != nil || changed {
		t.Fatalf("put same text = %v %v", changed, err)
	}
	if _, err := st.Put(id, "no frontmatter"); exitOf(err) != exitcode.ExitRefused {
		t.Fatalf("put without id: %v", err)
	}
	if _, err := Reindex(root, st); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, filepath.Join(root, ".rota", "MILESTONES.md")), "- M01 — Title") {
		t.Error("Reindex did not list the active milestone")
	}
}
