package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/proof"
)

// The file-mode proof store joins the proof package to the backlog: these
// tests pin that join (id judging, sidecar layout, legacy rows). The proof
// package's own tests run on plain values.

var proofDay = time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)

func exitCodeOf(err error) int {
	var ae *exitcode.Error
	if errors.As(err, &ae) {
		return ae.Exit
	}
	return 0
}

var proofRowDate = regexp.MustCompile(`(?m)^- \d{4}-\d{2}-\d{2} · `)

func proofMask(s string) string { return proofRowDate.ReplaceAllString(s, "- DATE · ") }

func proofGolden(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join("..", "proof", "testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// proofProject copies the proof fixture into a fresh .rota/, as the goldens were recorded.
func proofProject(t *testing.T) string {
	fixture := filepath.Join("..", "proof", "testdata", "fixture")
	root := t.TempDir()
	rota := filepath.Join(root, ".rota")
	err := filepath.Walk(fixture, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixture, p)
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
	root := proofProject(t)
	steps := []struct {
		id string
		o  proof.AddOpts
		ch bool
	}{
		{"B07", proof.AddOpts{Check: "unit  tests", Result: "PASS", Evidence: "go test ./... ok", Sha: "abc1234"}, true},
		{"B07", proof.AddOpts{Check: "unit  tests", Result: "PASS", Evidence: "go test ./... ok", Sha: "abc1234"}, false},
		{"B07", proof.AddOpts{Check: "smoke", Result: "FAIL", Evidence: "a · b · c", Sha: "-"}, true},
		{"F13", proof.AddOpts{Check: "lint", Result: "PASS", Evidence: "clean", Sha: "def5678"}, true},
		{"T03", proof.AddOpts{Check: "manual", Result: "PASS", Evidence: "looked", Sha: "111"}, true},
		{"B05", proof.AddOpts{Check: "archived", Result: "PASS", Evidence: "x", Sha: "222"}, true},
	}
	for _, s := range steps {
		if _, changed, err := proof.Add(fileProof(root), s.id, proofDay, s.o); err != nil || changed != s.ch {
			t.Fatalf("proof.Add(%s %+v) = %v %v, want changed=%v", s.id, s.o, changed, err, s.ch)
		}
	}
	// Rows go to the gitignored sidecar; the tracked detail file is never touched.
	for id, kind := range map[string]string{"B07": "bugs", "F13": "features", "T03": "tasks", "B05": "bugs"} {
		if _, err := os.Stat(filepath.Join(root, ".rota", kind, id+".proof")); err != nil {
			t.Errorf("%s: no proof sidecar: %v", id, err)
		}
		got, _ := os.ReadFile(filepath.Join(root, ".rota", kind, id+".md"))
		want, _ := os.ReadFile(filepath.Join("..", "proof", "testdata", "fixture", kind, id+".md"))
		if string(got) != string(want) {
			t.Errorf("%s: detail file changed:\n%s", id, got)
		}
	}
	for _, id := range []string{"B07", "F13", "T03", "B05", "B09"} {
		rows, lines, err := proof.Show(fileProof(root), id)
		if err != nil {
			t.Fatal(err)
		}
		want := proofGolden(t, "show-"+id+".txt")
		if got := proofMask(strings.Join(lines, "\n") + "\n"); (len(lines) > 0 || want != "") && got != want {
			t.Errorf("show %s = %q, want %q", id, got, want)
		}
		if wc := strings.TrimSpace(proofGolden(t, "count-"+id+".txt")); wc != strconv.Itoa(len(rows)) {
			t.Errorf("count %s = %d, want %s", id, len(rows), wc)
		}
	}
	rows, _, _ := proof.Show(fileProof(root), "B07")
	if rows[1].Check != "smoke" || rows[1].Result != "FAIL" || rows[1].Sha != "-" || rows[1].Evidence != "a · b · c" {
		t.Errorf("row split: %+v", rows[1])
	}
}

func TestAddCRLFDetailFile(t *testing.T) {
	root := proofProject(t)
	os.WriteFile(filepath.Join(root, ".rota/tasks/T03.md"), []byte("# T03\r\n\r\n## Proof\r\n\r\n- 2026-01-01 · a · PASS · s · e\r\n"), 0o644)
	if _, changed, err := proof.Add(fileProof(root), "T03", proofDay, proof.AddOpts{Check: "b", Result: "PASS", Evidence: "e", Sha: "s"}); err != nil || !changed {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, ".rota/tasks/T03.proof"))
	if strings.Contains(string(got), "\r") || strings.Count(string(got), "- ") != 2 {
		t.Errorf("got %q", got)
	}
}

// An unreadable proof file is not a missing one: nothing is overwritten.
func TestUnreadableDetailFileIsExit70(t *testing.T) {
	root := proofProject(t)
	p := filepath.Join(root, ".rota/bugs/B07.proof")
	if err := os.MkdirAll(p, 0o777); err != nil { // a directory: ReadFile fails with EISDIR
		t.Fatal(err)
	}
	if _, _, err := proof.Add(fileProof(root), "B07", proofDay, proof.AddOpts{Check: "c", Result: "PASS", Evidence: "e", Sha: "s"}); exitCodeOf(err) != 70 {
		t.Errorf("add: %v", err)
	}
	if _, _, err := proof.Show(fileProof(root), "B07"); exitCodeOf(err) != 70 {
		t.Errorf("show: %v", err)
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		t.Error("the unreadable path was replaced")
	}
}

// The store judges ids through the backlog: malformed is a usage error, an
// unknown item is a resolution error.
func TestFileProofJudgesIDs(t *testing.T) {
	root := proofProject(t)
	ok := proof.AddOpts{Check: "c", Result: "PASS", Evidence: "e", Sha: "s"}
	for _, c := range []struct {
		id   string
		exit int
	}{{"X01", 2}, {"B7x", 2}, {"b07", 2}, {"B99", 3}} {
		if _, _, err := proof.Add(fileProof(root), c.id, proofDay, ok); exitCodeOf(err) != c.exit {
			t.Errorf("Add(%s) = %v, want exit %d", c.id, err, c.exit)
		}
	}
	if _, _, err := proof.Show(fileProof(root), "nope"); exitCodeOf(err) != 2 {
		t.Errorf("show malformed: %v", err)
	}
}

// proofSha is the caller's side of the seam: a given sha wins, else HEAD, else "-".
func TestProofShaResolvesHead(t *testing.T) {
	dir := t.TempDir()
	gittest.Init(t, dir, "main")
	gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "i")
	c := &Ctx{Deps: defaultDeps()}
	if got := proofSha(c, dir, "  abc1234 "); got != "abc1234" {
		t.Errorf("given sha = %q", got)
	}
	if got := proofSha(c, dir, ""); len(got) < 7 || got == "-" {
		t.Errorf("head = %q", got)
	}
	if got := proofSha(c, t.TempDir(), ""); got != "-" {
		t.Errorf("no repository = %q, want -", got)
	}
}

// File-mode "done requires proof" through the real proof store and counter.
func TestFileDoneRequiresProof(t *testing.T) {
	root := proofProject(t)
	be := &backlog.File{Root: root, CountProof: proof.CountRows}
	in := backlog.CompleteInput{Commit: "abc1234", Date: "2026-10-05", Reason: "done"}
	if _, err := be.Complete("B07", in); !errors.Is(err, backlog.ErrProofMissing) {
		t.Fatalf("without proof: %v", err)
	}
	o := proof.AddOpts{Check: "unit tests", Result: "PASS", Evidence: "go test ok", Sha: "abc1234"}
	if _, _, err := proof.Add(fileProof(root), "B07", proofDay, o); err != nil {
		t.Fatal(err)
	}
	if changed, err := be.Complete("B07", in); err != nil || !changed {
		t.Fatalf("with proof: changed=%v err=%v", changed, err)
	}
}

// The main checkout and a linked worktree share the issue note, so they must
// lock appends on the same directory: the proof package takes it from here.
func TestItemNoteLockDirIsSharedAcrossWorktrees(t *testing.T) {
	repo := gittest.NewRepo(t, "main")
	wt := filepath.Join(t.TempDir(), "wt")
	gittest.Run(t, repo, "worktree", "add", "-q", wt, "-b", "other")
	c := &Ctx{Deps: defaultDeps()}
	a, b := itemNoteLockDir(c, repo), itemNoteLockDir(c, wt)
	if a != b || a == filepath.Join(repo, ".rota") {
		t.Errorf("lock dirs differ: %q vs %q", a, b)
	}
}
