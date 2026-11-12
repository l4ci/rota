package initproj

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/rotatree"
)

// The one seam: both init paths merge wanted lines into a .gitignore on disk,
// so both must agree on CRLF input and on `.worktrees` spellings.

func TestGitignoreCRLFAgreesAcrossInitPaths(t *testing.T) {
	// project init: managed header and lines written with CRLF are recognised
	block := strings.ReplaceAll(strings.Join(ignoreLines, "\n")+"\n", "\n", "\r\n")
	got := MergeGitignore(block+".worktrees/\r\n", true)
	if got != block+".worktrees/\r\n" {
		t.Errorf("project init rewrote a complete CRLF file:\n got %q", got)
	}

	// umbrella init: the appended block keeps the file's line ends
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	gi := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gi, []byte("a\r\n.claude/\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Seed(dir, "web", "web"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(gi)
	if s := string(b); strings.Contains(strings.ReplaceAll(s, "\r\n", ""), "\n") {
		t.Errorf("umbrella init mixed line ends: %q", s)
	}
}

func TestGitignoreWorktreesSpellingsAgree(t *testing.T) {
	for _, spelling := range []string{".worktrees", ".worktrees/", "/.worktrees", "/.worktrees/"} {
		in := "x\n" + spelling + "\r\n"
		if got := mergeIgnore(in, true, "# h", []string{".worktrees/"}); got != in {
			t.Errorf("%q: spelling not recognised: %q", spelling, got)
		}
	}
}

func TestManagedEntriesComeFromRotatree(t *testing.T) {
	if got := ignoreLines[1]; got != rotatree.Rel(rotatree.StatusFile) {
		t.Errorf("first entry = %q", got)
	}
	if worktreesLine != rotatree.WorktreeRoot+"/" {
		t.Errorf("worktrees line = %q", worktreesLine)
	}
}

func TestGitignoreCRLFInsertUnderHeaderKeepsLineEnds(t *testing.T) {
	in := rotaHeader + "\r\n" + ignoreLines[1] + "\r\n.worktrees/\r\n"
	got := MergeGitignore(in, true)
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("mixed line ends: %q", got)
	}
	if !strings.Contains(got, ignoreLines[2]+"\r\n") {
		t.Errorf("missing line not inserted: %q", got)
	}
}
