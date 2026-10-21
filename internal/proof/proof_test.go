package proof

import (
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func exitOf(err error) int {
	var ae *exitcode.Error
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

// The shown rows must stay byte-identical to what the retired proof add and
// show helpers produced (testdata/golden, frozen, changed only by reviewed
// edit); the rows now live in the sidecar, and the detail file stays as it was.
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
		if _, changed, err := Add(Files(root), root, s.id, proofDay, s.o); err != nil || changed != s.ch {
			t.Fatalf("Add(%s %+v) = %v %v, want changed=%v", s.id, s.o, changed, err, s.ch)
		}
	}
	// Rows go to the gitignored sidecar; the tracked detail file is never touched.
	for id, kind := range map[string]string{"B07": "bugs", "F13": "features", "T03": "tasks", "B05": "bugs"} {
		if _, err := os.Stat(filepath.Join(root, ".rota", kind, id+".proof")); err != nil {
			t.Errorf("%s: no proof sidecar: %v", id, err)
		}
		got, _ := os.ReadFile(filepath.Join(root, ".rota", kind, id+".md"))
		want, _ := os.ReadFile(filepath.Join("testdata", "fixture", kind, id+".md"))
		if string(got) != string(want) {
			t.Errorf("%s: detail file changed:\n%s", id, got)
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
		if _, _, err := Add(Files(root), root, c.id, proofDay, c.o); exitOf(err) != c.exit {
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
	row, _, err := Add(Files(root), root, "B07", proofDay, AddOpts{Check: "c", Result: "PASS", Evidence: "e"})
	if err != nil || len(row.Sha) < 7 || row.Sha == "-" {
		t.Fatalf("sha = %q %v", row.Sha, err)
	}
	// No repository: "-".
	root2 := project(t)
	if row, _, _ := Add(Files(root2), root2, "B07", proofDay, AddOpts{Check: "c", Result: "PASS", Evidence: "e"}); row.Sha != "-" {
		t.Errorf("no-git sha = %q", row.Sha)
	}
}

func TestAddCRLFDetailFile(t *testing.T) {
	root := project(t)
	os.WriteFile(filepath.Join(root, ".rota/tasks/T03.md"), []byte("# T03\r\n\r\n## Proof\r\n\r\n- 2026-01-01 · a · PASS · s · e\r\n"), 0o644)
	if _, changed, err := Add(Files(root), root, "T03", proofDay, AddOpts{"b", "PASS", "e", "s"}); err != nil || !changed {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, ".rota/tasks/T03.proof"))
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
			_, ch, err := Add(Files(root), root, "B07", proofDay, AddOpts{"c", "PASS", "e", "s"})
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

// An unreadable proof file is not a missing one: nothing is overwritten.
func TestUnreadableDetailFileIsExit70(t *testing.T) {
	root := project(t)
	p := filepath.Join(root, ".rota/bugs/B07.proof")
	if err := os.MkdirAll(p, 0o777); err != nil { // a directory: ReadFile fails with EISDIR
		t.Fatal(err)
	}
	if _, _, err := Add(Files(root), root, "B07", proofDay, AddOpts{"c", "PASS", "e", "s"}); exitOf(err) != 70 {
		t.Errorf("add: %v", err)
	}
	if _, _, err := Show(Files(root), "B07"); exitOf(err) != 70 {
		t.Errorf("show: %v", err)
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		t.Error("the unreadable path was replaced")
	}
}

var proofDay = time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)

func TestAddDatesRowWithTheGivenDay(t *testing.T) {
	root := project(t)
	row, _, err := Add(Files(root), root, "B07", proofDay, AddOpts{Check: "c", Result: "PASS", Evidence: "e", Sha: "abc1234"})
	if err != nil || row.Date != "2026-03-04" {
		t.Fatalf("row = %+v %v", row, err)
	}
}

func TestProvesAtHead(t *testing.T) {
	const head = "abc1234def5678"
	pass := Row{Check: "go test ./x", Result: "PASS", Sha: "abc1234"}
	fail := Row{Check: "go  test ./x", Result: "FAIL", Sha: "abc1234"}
	for _, tc := range []struct {
		name  string
		rows  []Row
		head  string
		check string
		want  bool
	}{
		{"pass at head", []Row{pass}, head, "go test ./x", true},
		{"pass then fail at head", []Row{pass, fail}, head, "go test ./x", false},
		{"fail then pass at head", []Row{fail, pass}, head, "go test ./x", true},
		{"pass at another sha", []Row{{Check: "go test ./x", Result: "PASS", Sha: "ffff000"}}, head, "go test ./x", false},
		{"other check", []Row{pass}, head, "other", false},
		{"row sha under 7 chars", []Row{{Check: "c", Result: "PASS", Sha: "abc123"}}, head, "c", false},
		{"head under 7 chars", []Row{pass}, "abc123", "go test ./x", false},
		{"head shorter than row sha", []Row{{Check: "c", Result: "PASS", Sha: head}}, "abc1234", "c", true},
		{"no rows", nil, head, "c", false},
	} {
		if got := Proves(tc.rows, tc.head, tc.check); got != tc.want {
			t.Errorf("%s: Proves = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Rows an older rota wrote into the detail file still count, and the first
// write seeds the sidecar with them instead of dropping them.
func TestLegacyDetailRowsAreReadAndSeedTheSidecar(t *testing.T) {
	root := project(t)
	detail := filepath.Join(root, ".rota/features/F13.md")
	before, _ := os.ReadFile(detail)
	rows, _, err := Show(Files(root), "F13")
	if err != nil || len(rows) != 1 || rows[0].Check != "seed" {
		t.Fatalf("legacy read = %+v %v", rows, err)
	}
	if _, _, err := Add(Files(root), root, "F13", proofDay, AddOpts{"lint", "PASS", "clean", "def5678"}); err != nil {
		t.Fatal(err)
	}
	if rows, _, _ := Show(Files(root), "F13"); len(rows) != 2 || rows[0].Check != "seed" || rows[1].Check != "lint" {
		t.Errorf("after add = %+v", rows)
	}
	side, _ := os.ReadFile(filepath.Join(root, ".rota/features/F13.proof"))
	if strings.Contains(string(side), "Notes") || strings.Contains(string(side), "after") {
		t.Errorf("sidecar copied more than the proof rows:\n%s", side)
	}
	if after, _ := os.ReadFile(detail); string(after) != string(before) {
		t.Errorf("detail file changed:\n%s", after)
	}
}
