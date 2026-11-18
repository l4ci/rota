package backlog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// unreadableDetail makes B01's detail path a directory: reading it fails with
// something other than ErrNotExist, without depending on file modes.
func unreadableDetail(t *testing.T) *File {
	t.Helper()
	f := fileBackend(t, "## Bugs\n- **[B01] [P1] a.** x\n")
	if err := os.MkdirAll(f.rota("bugs", "B01.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFileDetailReadFailure(t *testing.T) {
	f := unreadableDetail(t)
	if text, ok, err := f.Detail("B01"); err == nil || ok || text != "" {
		t.Fatalf("unreadable detail = %q %v %v, want an error", text, ok, err)
	}
	// Absence is not an error: missing file, empty ref, no type.
	for _, ref := range []string{"B02", "", "9"} {
		if _, ok, err := f.Detail(ref); ok || err != nil {
			t.Errorf("Detail(%q) = ok %v err %v, want absent without error", ref, ok, err)
		}
	}
}

func TestReadyAndSetDetailSurfaceReadFailure(t *testing.T) {
	f := unreadableDetail(t)
	if _, err := f.Ready("B01"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("Ready = %v, want the read error", err)
	}
	if err := f.SetDetail("B01", "x"); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("SetDetail = %v, want the read error", err)
	}
	if _, err := f.Comments("B01", ""); err == nil {
		t.Error("Comments dropped the read error")
	}
}

func TestDriftGitLogFailure(t *testing.T) {
	f := fileBackend(t, "## Bugs\n- **[B01] [P1] a.** x\n")
	gittest.Init(t, f.Root, "main")
	h1 := gittest.Commit(t, f.Root, "chore: init", "a.txt", "x")
	gittest.Commit(t, f.Root, "fix: [B01] later", "b.txt", "x")
	// Lose the parent commit object: HEAD still resolves, `git log` cannot walk.
	obj := filepath.Join(f.Root, ".git", "objects", h1[:2], h1[2:])
	if full := gitFullObject(t, f.Root, h1); full != "" {
		obj = full
	}
	if err := os.Chmod(obj, 0o644); err == nil {
		os.Remove(obj)
	} else {
		t.Skipf("cannot locate loose object: %v", err)
	}
	if _, _, err := f.Drift([]Target{{Dir: f.Root}}); err == nil {
		t.Fatal("drift swallowed the git log failure")
	}
}

func gitFullObject(t *testing.T, dir, short string) string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, ".git", "objects", short[:2], "*"))
	for _, m := range matches {
		if strings.HasPrefix(filepath.Base(m), short[2:]) {
			return m
		}
	}
	return ""
}

func TestLoadSummaryInputsReadFailure(t *testing.T) {
	f := fileBackend(t, viewBacklog)
	for _, name := range []string{"KNOWLEDGE.md", "DECISIONS.md", "ARCHIVE.md"} {
		dir := filepath.Join(f.Root, ".rota", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadSummaryInputs(f.Root, nil, false); err == nil {
			t.Errorf("%s read failure was dropped", name)
		}
		os.Remove(dir)
	}
	if _, err := LoadSummaryInputs(f.Root, nil, false); err != nil {
		t.Errorf("missing files must stay tolerated: %v", err)
	}
}
