package design

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/l4ci/rota/internal/artifact"
)

func exitOf(err error) int {
	var ae *artifact.Error
	if errors.As(err, &ae) {
		return ae.Exit
	}
	return 0
}

var created = regexp.MustCompile(`(?m)^created: \d{4}-\d{2}-\d{2}$`)

func project(t *testing.T) string {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".rota"), 0o777)
	return root
}

// testdata/golden holds what the retired design-add helper wrote (date
// masked). It is frozen: the files must stay byte-identical, and change only by
// reviewed edit.
func TestAddMatchesOldHelper(t *testing.T) {
	root := project(t)
	for id, title := range map[string]string{"B07": "Title: with colon & é", "F12": "Second"} {
		if err := Add(root, id, title); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(path(root, id))
		want, _ := os.ReadFile(filepath.Join("testdata", "golden", id+".md"))
		if g := created.ReplaceAllString(string(got), "created: DATE"); g != string(want) {
			t.Errorf("%s differs from golden:\n%s", id, g)
		}
	}
}

func TestLifecycleAndExits(t *testing.T) {
	root := project(t)
	for _, id := range []string{"S01", "M01", "B7", "b07", "../x"} {
		if err := Add(root, id, "t"); exitOf(err) != 2 {
			t.Errorf("Add(%q) = %v, want exit 2", id, err)
		}
	}
	Add(root, "B07", "t")
	if err := Add(root, "B07", "again"); exitOf(err) != 4 {
		t.Errorf("duplicate add: %v", err)
	}
	if _, err := Show(root, "F99"); exitOf(err) != 3 {
		t.Errorf("show missing: %v", err)
	}
	if _, err := Put(root, "F99", "x"); exitOf(err) != 3 {
		t.Errorf("put missing: %v", err)
	}
	if changed, err := Put(root, "B07", "new text\r\nCRLF kept\r\n"); err != nil || !changed {
		t.Fatalf("put: %v %v", changed, err)
	}
	if changed, _ := Put(root, "B07", "new text\r\nCRLF kept\r\n"); changed {
		t.Error("identical put reported changed")
	}
	if got, _ := Show(root, "B07"); got != "new text\r\nCRLF kept\r\n" {
		t.Errorf("show = %q", got)
	}
	if err := Rm(root, "B07"); err != nil {
		t.Fatal(err)
	}
	if err := Rm(root, "B07"); exitOf(err) != 3 {
		t.Errorf("second rm: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota/designs/F99.md.lock")); err == nil {
		t.Error("failed put left a lock file")
	}
}

func TestListDefaultsAndSkips(t *testing.T) {
	root := project(t)
	dir := filepath.Join(root, ".rota", "designs")
	os.MkdirAll(dir, 0o777)
	os.WriteFile(filepath.Join(dir, "plain.md"), []byte("# nothing\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "F02.md"), []byte("---\ntitle: T\n---\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "B01.md"), []byte("---\r\nid: B01\r\nstatus: done\r\ncreated: 2026-01-01\r\n---\r\n"), 0o644)
	list, _ := List(root)
	if len(list) != 2 || list[0] != (Entry{"B01", "", "done", "2026-01-01"}) || list[1] != (Entry{"F02", "T", "draft", ""}) {
		t.Fatalf("list = %+v", list)
	}
}

func TestAddConcurrent(t *testing.T) {
	root := project(t)
	var ok, refused atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch err := Add(root, "B07", "t"); {
			case err == nil:
				ok.Add(1)
			case exitOf(err) == 4:
				refused.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || refused.Load() != 7 {
		t.Fatalf("ok=%d refused=%d", ok.Load(), refused.Load())
	}
}

// Three amend calls against the frozen output of the retired helper.
func TestAmendMatchesOldHelper(t *testing.T) {
	root := project(t)
	Add(root, "B07", "Title: with colon & é")
	steps := []struct{ heading, mode, text, golden string }{
		{"Goal", "append", "first line\nsecond line\n\n", ""},
		{"Open questions", "append", "- one more", "B07.amend-append.md"},
		{"Design", "replace", "replaced body", "B07.amend-replace.md"},
	}
	for _, s := range steps {
		if changed, err := Amend(root, "B07", s.heading, s.mode, s.text); err != nil || !changed {
			t.Fatalf("Amend(%s): %v %v", s.heading, changed, err)
		}
		if s.golden == "" {
			continue
		}
		got, _ := os.ReadFile(path(root, "B07"))
		if g := created.ReplaceAllString(string(got), "created: DATE"); g != golden(t, s.golden) {
			t.Errorf("%s differs from golden:\n%s", s.golden, g)
		}
	}
}

func golden(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join("testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAmendExits(t *testing.T) {
	root := project(t)
	if _, err := Amend(root, "B07", "Goal", "append", "x"); exitOf(err) != 3 {
		t.Errorf("missing design: %v", err)
	}
	Add(root, "B07", "t")
	if _, err := Amend(root, "B07", "No such", "append", "x"); exitOf(err) != 3 {
		t.Errorf("missing section: %v", err)
	}
	if _, err := Amend(root, "B07", "Goal", "prepend", "x"); exitOf(err) != 2 {
		t.Errorf("bad mode: %v", err)
	}
	if _, err := Amend(root, "S01", "Goal", "append", "x"); exitOf(err) != 2 {
		t.Errorf("bad id: %v", err)
	}
	Amend(root, "B07", "Goal", "replace", "same")
	if changed, _ := Amend(root, "B07", "Goal", "replace", "same\n\n"); changed {
		t.Error("amend with identical result reported changed")
	}
}
