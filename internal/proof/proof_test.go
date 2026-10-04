package proof

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

var rowDate = regexp.MustCompile(`(?m)^- \d{4}-\d{2}-\d{2} · `)

func mask(s string) string { return rowDate.ReplaceAllString(s, "- DATE · ") }

func golden(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join("testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// project copies testdata/fixture into a fresh .rota/, as the goldens were recorded.
func project(t *testing.T) string {
	root := t.TempDir()
	rota := filepath.Join(root, ".rota")
	err := filepath.Walk(filepath.Join("testdata", "fixture"), func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(filepath.Join("testdata", "fixture"), p)
		if fi.IsDir() {
			return os.MkdirAll(filepath.Join(rota, rel), 0o777)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(rota, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// The detail files and the shown rows must stay byte-identical to what the
// retired proof add and show helpers produced (testdata/golden, frozen,
// changed only by reviewed edit).
func TestAddAndShowMatchOldHelpers(t *testing.T) {
	root := project(t)
	steps := []struct {
		id string
		o  AddOpts
		ch bool
	}{
		{"B07", AddOpts{"unit  tests", "PASS", "go test ./... ok", "abc1234"}, true},
		{"B07", AddOpts{"unit  tests", "PASS", "go test ./... ok", "abc1234"}, false},
		{"B07", AddOpts{"smoke", "FAIL", "a · b · c", "-"}, true},
		{"F13", AddOpts{"lint", "PASS", "clean", "def5678"}, true},
		{"T03", AddOpts{"manual", "PASS", "looked", "111"}, true},
		{"B05", AddOpts{"archived", "PASS", "x", "222"}, true},
	}
	for _, s := range steps {
		if _, changed, err := Add(Files(root), root, s.id, s.o); err != nil || changed != s.ch {
			t.Fatalf("Add(%s %+v) = %v %v, want changed=%v", s.id, s.o, changed, err, s.ch)
		}
	}
	for id, kind := range map[string]string{"B07": "bugs", "F13": "features", "T03": "tasks", "B05": "bugs"} {
		got, _ := os.ReadFile(filepath.Join(root, ".rota", kind, id+".md"))
		if mask(string(got)) != golden(t, id+".md") {
			t.Errorf("%s differs from golden:\n%s", id, got)
		}
	}
	for _, id := range []string{"B07", "F13", "T03", "B05", "B09"} {
		rows, lines, err := Show(Files(root), id)
		if err != nil {
			t.Fatal(err)
		}
		want := golden(t, "show-"+id+".txt")
		if got := mask(strings.Join(lines, "\n") + "\n"); (len(lines) > 0 || want != "") && got != want {
			t.Errorf("show %s = %q, want %q", id, got, want)
		}
		if wc := strings.TrimSpace(golden(t, "count-"+id+".txt")); wc != strconv.Itoa(len(rows)) {
			t.Errorf("count %s = %d, want %s", id, len(rows), wc)
		}
	}
	rows, _, _ := Show(Files(root), "B07")
	if rows[1].Check != "smoke" || rows[1].Result != "FAIL" || rows[1].Sha != "-" || rows[1].Evidence != "a · b · c" {
		t.Errorf("row split: %+v", rows[1])
	}
}

func TestAddExits(t *testing.T) {
	root := project(t)
	ok := AddOpts{"c", "PASS", "e", "s"}
	for _, c := range []struct {
		id   string
		o    AddOpts
		exit int
	}{
		{"X01", ok, 2}, {"B7x", ok, 2}, {"b07", ok, 2},
		{"B07", AddOpts{"", "PASS", "e", "s"}, 2},
		{"B07", AddOpts{"c", "PASS", " ", "s"}, 2},
		{"B07", AddOpts{"c", "pass", "e", "s"}, 2},
		{"B07", AddOpts{"c", "", "e", "s"}, 2},
		{"B99", ok, 3},
	} {
		if _, _, err := Add(Files(root), root, c.id, c.o); exitOf(err) != c.exit {
			t.Errorf("Add(%s %+v) = %v, want exit %d", c.id, c.o, err, c.exit)
		}
	}
	if _, _, err := Show(Files(root), "nope"); exitOf(err) != 2 {
		t.Errorf("show malformed: %v", err)
	}
}

func TestDefaultShaFromGit(t *testing.T) {
	root := project(t)
	for _, a := range [][]string{{"init", "-q"}, {"-c", "user.email=a@b", "-c", "user.name=n", "commit", "-q", "--allow-empty", "-m", "i"}} {
		c := exec.Command("git", a...)
		c.Dir = root
		c.Run()
	}
	row, _, err := Add(Files(root), root, "B07", AddOpts{Check: "c", Result: "PASS", Evidence: "e"})
	if err != nil || len(row.Sha) < 7 || row.Sha == "-" {
		t.Fatalf("sha = %q %v", row.Sha, err)
	}
	// No repository: "-".
	root2 := project(t)
	if row, _, _ := Add(Files(root2), root2, "B07", AddOpts{Check: "c", Result: "PASS", Evidence: "e"}); row.Sha != "-" {
		t.Errorf("no-git sha = %q", row.Sha)
	}
}

func TestAddCRLFDetailFile(t *testing.T) {
	root := project(t)
	p := filepath.Join(root, ".rota/tasks/T03.md")
	os.WriteFile(p, []byte("# T03\r\n\r\n## Proof\r\n\r\n- 2026-01-01 · a · PASS · s · e\r\n"), 0o644)
	if _, changed, err := Add(Files(root), root, "T03", AddOpts{"b", "PASS", "e", "s"}); err != nil || !changed {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if strings.Contains(string(got), "\r") || strings.Count(string(got), "- ") != 2 {
		t.Errorf("got %q", got)
	}
}

func TestAddConcurrentIdenticalRows(t *testing.T) {
	root := project(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	changed := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ch, err := Add(Files(root), root, "B07", AddOpts{"c", "PASS", "e", "s"})
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			if ch {
				changed++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if rows, _, _ := Show(Files(root), "B07"); changed != 1 || len(rows) != 1 {
		t.Fatalf("changed=%d rows=%d, want 1 and 1", changed, len(rows))
	}
}

// An unreadable detail file is not a missing one: nothing is overwritten.
func TestUnreadableDetailFileIsExit70(t *testing.T) {
	root := project(t)
	p := filepath.Join(root, ".rota/bugs/B07.md")
	if err := os.MkdirAll(p, 0o777); err != nil { // a directory: ReadFile fails with EISDIR
		t.Fatal(err)
	}
	if _, _, err := Add(Files(root), root, "B07", AddOpts{"c", "PASS", "e", "s"}); exitOf(err) != 70 {
		t.Errorf("add: %v", err)
	}
	if _, _, err := Show(Files(root), "B07"); exitOf(err) != 70 {
		t.Errorf("show: %v", err)
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		t.Error("the unreadable path was replaced")
	}
}
