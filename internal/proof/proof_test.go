package proof

import (
	"errors"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// flat is the file-mode store over plain values: a temp dir, the sidecar at
// <dir>/<id>.proof, a detail file at <dir>/<id>.md that holds legacy rows, and
// no item model (every id names an item). dir is the second result.
func flat(t *testing.T) Store {
	st, _ := flatDir(t)
	return st
}

func flatDir(t *testing.T) (Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := func(id string) string { return filepath.Join(dir, id+".proof") }
	return Files(Sidecar{
		Path:  func(id string) (string, error) { return path(id), nil },
		Title: func(string) (string, error) { return "", nil },
		Text: func(id string) (string, bool, error) {
			for _, p := range []string{path(id), filepath.Join(dir, id+".md")} {
				b, err := os.ReadFile(p)
				if os.IsNotExist(err) {
					continue
				}
				return string(b), err == nil, err
			}
			return "", false, nil
		},
	}), dir
}

func exitOf(err error) int {
	var ae *exitcode.Error
	if errors.As(err, &ae) {
		return ae.Exit
	}
	return 0
}

func TestAddConcurrentIdenticalRows(t *testing.T) {
	st := flat(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	changed := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, ch, err := Add(st, "B07", proofDay, AddOpts{"c", "PASS", "e", "s"})
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
	if rows, _, _ := Show(st, "B07"); changed != 1 || len(rows) != 1 {
		t.Fatalf("changed=%d rows=%d, want 1 and 1", changed, len(rows))
	}
}

var proofDay = time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)

func TestAddDatesRowWithTheGivenDay(t *testing.T) {
	row, _, err := Add(flat(t), "B07", proofDay, AddOpts{Check: "c", Result: "PASS", Evidence: "e", Sha: "abc1234"})
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

type memStore struct{ content string }

func (m *memStore) Update(_ string, fn func(string) (string, bool, error)) error {
	out, changed, err := fn(m.content)
	if changed {
		m.content = out
	}
	return err
}

func (m *memStore) Read(string) (string, bool, error) { return m.content, true, nil }

// Mark follows Latest's sha rule: an empty or under-7-char sha names no commit,
// so it marks nothing, while a prefix of a row's sha either way still matches.
func TestMarkUsesTheSharedShaRule(t *testing.T) {
	const row = "- 2026-01-01 · go test ./x · PASS · abc1234def5678 · detail"
	for _, tc := range []struct {
		name string
		sha  string
		want bool
	}{
		{"empty sha", "", false},
		{"short sha", "abc123", false},
		{"seven char prefix", "abc1234", true},
		{"longer than row sha", "abc1234def5678aa", true},
		{"other sha", "ffff0000", false},
	} {
		s := &memStore{content: "## Proof\n\n" + row + "\n"}
		found, err := Mark(s, "1", tc.sha, "go test ./x", "targeted")
		if err != nil || found != tc.want {
			t.Errorf("%s: Mark = %v, %v; want %v", tc.name, found, err, tc.want)
		}
		if marked := strings.Contains(s.content, "detail targeted"); marked != tc.want {
			t.Errorf("%s: row marked = %v, want %v", tc.name, marked, tc.want)
		}
	}
}

// Mark edits only rows inside "## Proof": a look-alike line elsewhere in the
// item is left alone, even when it is the later of two matching lines.
func TestMarkIgnoresLookAlikeOutsideProofSection(t *testing.T) {
	const row = "- 2026-01-01 · go test ./x · PASS · abc1234def5678 · detail"
	before := "## Notes\n\n" + row + "\n\n## Proof\n\n" + row + "\n\n## Later\n\n" + row + "\n"
	s := &memStore{content: before}
	found, err := Mark(s, "1", "abc1234", "go test ./x", "targeted")
	if err != nil || !found {
		t.Fatalf("Mark = %v, %v; want found", found, err)
	}
	want := "## Notes\n\n" + row + "\n\n## Proof\n\n" + row + " targeted\n\n## Later\n\n" + row + "\n"
	if s.content != want {
		t.Errorf("content =\n%q\nwant\n%q", s.content, want)
	}

	s = &memStore{content: "## Notes\n\n" + row + "\n"}
	if found, err := Mark(s, "1", "abc1234", "go test ./x", "targeted"); err != nil || found {
		t.Errorf("no Proof section: Mark = %v, %v; want not found", found, err)
	}
}

// The caller resolves HEAD; Add never asks git, so a row without a sha is "-".
func TestAddWithoutShaStoresDash(t *testing.T) {
	row, _, err := Add(flat(t), "B07", proofDay, AddOpts{Check: "c", Result: "PASS", Evidence: "e"})
	if err != nil || row.Sha != "-" {
		t.Fatalf("row = %+v %v", row, err)
	}
}

func TestAddRejectsBadInput(t *testing.T) {
	st := flat(t)
	for _, o := range []AddOpts{{"", "PASS", "e", "s"}, {"c", "PASS", " ", "s"}, {"c", "pass", "e", "s"}, {"c", "", "e", "s"}} {
		if _, _, err := Add(st, "B07", proofDay, o); exitOf(err) != 2 {
			t.Errorf("Add(%+v) = %v, want exit 2", o, err)
		}
	}
}

// Rows an older rota wrote into the detail file still count, and the first
// write seeds the sidecar with them instead of dropping them.
func TestLegacyRowsAreReadAndSeedTheSidecar(t *testing.T) {
	st, dir := flatDir(t)
	detail := filepath.Join(dir, "F13.md")
	legacy := "# F13\n\n## Proof\n\n- 2026-01-01 · seed · PASS · abc1234 · seeded\n\n## Notes\n\nafter\n"
	if err := os.WriteFile(detail, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if rows, _, err := Show(st, "F13"); err != nil || len(rows) != 1 || rows[0].Check != "seed" {
		t.Fatalf("legacy read = %+v %v", rows, err)
	}
	if _, _, err := Add(st, "F13", proofDay, AddOpts{"lint", "PASS", "clean", "def5678"}); err != nil {
		t.Fatal(err)
	}
	if rows, _, _ := Show(st, "F13"); len(rows) != 2 || rows[0].Check != "seed" || rows[1].Check != "lint" {
		t.Errorf("after add = %+v", rows)
	}
	side, _ := os.ReadFile(filepath.Join(dir, "F13.proof"))
	if strings.Contains(string(side), "Notes") || strings.Contains(string(side), "after") {
		t.Errorf("sidecar copied more than the proof rows:\n%s", side)
	}
	if after, _ := os.ReadFile(detail); string(after) != legacy {
		t.Errorf("detail file changed:\n%s", after)
	}
}
