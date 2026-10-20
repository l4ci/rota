package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ms "github.com/l4ci/rota/internal/milestone"
)

// gistProject is a file-mode project with milestone M01, a research item B02
// and an ordinary item B01.
func gistProject(t *testing.T) string {
	t.Helper()
	root := trackerProject(t, "")
	rota := filepath.Join(root, ".rota")
	backlog := "# TODO\n\n## Bugs\n- **[B01] [P1] First.** x\n- **[B02] [P1] Which lib?** x Milestone: M01 Decision: research\n\n## Completed\n"
	if err := os.WriteFile(filepath.Join(rota, "BACKLOG.md"), []byte(backlog), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rota, "milestones"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rota, "milestones", "M01.md"), []byte(ms.Stub("M01", "Fog", "Find out.", nil)), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestItemCompleteGist(t *testing.T) {
	t.Run("a decision item appends its gist to the milestone", func(t *testing.T) {
		root := gistProject(t)
		code, _, stderr := rotaRun(t, "--json", "-C", root, "item", "complete", "B02", "--commit", "abc", "--no-proof", "--gist", "use X")
		if code != 0 {
			t.Fatalf("code=%d %s", code, stderr)
		}
		m, _ := os.ReadFile(filepath.Join(root, ".rota", "milestones", "M01.md"))
		if !strings.Contains(string(m), "## Decisions\n\n- B02: use X\n") {
			t.Errorf("decision line missing:\n%s", m)
		}
	})
	t.Run("a decision item without --gist is refused and stays open", func(t *testing.T) {
		root := gistProject(t)
		code, _, stderr := rotaRun(t, "--json", "-C", root, "item", "complete", "B02", "--commit", "abc", "--no-proof")
		if code != ExitFailed || !strings.Contains(stderr, "--gist") {
			t.Fatalf("code=%d %s", code, stderr)
		}
		b, _ := os.ReadFile(filepath.Join(root, ".rota", "BACKLOG.md"))
		if strings.Contains(string(b), "~~**[B02]") {
			t.Errorf("B02 must stay open:\n%s", b)
		}
	})
	t.Run("--gist on an ordinary item is refused", func(t *testing.T) {
		root := gistProject(t)
		code, _, stderr := rotaRun(t, "--json", "-C", root, "item", "complete", "B01", "--commit", "abc", "--no-proof", "--gist", "x")
		if code != ExitFailed || !strings.Contains(stderr, "not a decision item") {
			t.Fatalf("code=%d %s", code, stderr)
		}
	})
}
