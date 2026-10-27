package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMilestoneShippedSummary(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		root := trackerProject(t, "")
		if code, _, stderr := rotaIn(t, root, "milestone", "add", "--title", "First", "--summary", "Ship it"); code != 0 {
			t.Fatalf("add: %d %s", code, stderr)
		}
		body := "---\nid: M01\ntitle: First\nstatus: planned\n---\n\n## Acceptance\n\n- [x] AC-1: ready\n\n## Decisions\n\n- B01: keep it small\n"
		for _, args := range [][]string{
			{"milestone", "put", "M01", "--body-file", bodyFile(t, body)},
			{"item", "field", "set", "B01", "--name", "milestone", "--value", "M01"},
			{"item", "complete", "B01", "--reason", "dropped", "--commit", "abc1234"},
			{"milestone", "status", "M01", "--to", "shipped"},
		} {
			if code, _, stderr := rotaIn(t, root, args...); code != 0 {
				t.Fatalf("%v: %d %s", args, code, stderr)
			}
		}
		text, err := os.ReadFile(filepath.Join(root, ".rota/milestones/M01.md"))
		if err != nil {
			t.Fatal(err)
		}
		i := strings.Index(string(text), "## Shipped\n")
		if i < 0 {
			t.Fatalf("missing closing summary: %s", text)
		}
		for _, want := range []string{"B01", "First", "AC-1: met", "keep it small"} {
			if !strings.Contains(string(text)[i:], want) {
				t.Errorf("summary lacks %q: %s", want, text)
			}
		}
		if code, _, stderr := rotaIn(t, root, "milestone", "status", "M01", "--to", "shipped"); code != 0 {
			t.Fatalf("retry: %d %s", code, stderr)
		}
		again, _ := os.ReadFile(filepath.Join(root, ".rota/milestones/M01.md"))
		if string(again) != string(text) {
			t.Fatal("retry changed closing record")
		}
	})
	t.Run("issues", func(t *testing.T) {
		root := trackerProject(t, issuesConfig)
		fake := msFixture()
		deps := withTracker(t, fake)
		fake.Issues[0].State = "closed"
		fake.Issues[2].Body += "\n\n## Acceptance\n\n- [x] AC-1: ready\n\n## Decisions\n\n- #7: keep it small\n"
		code, _, stderr := issueRunWith(t, deps, root, "milestone", "status", "M02", "--to", "shipped")
		if code != 0 {
			t.Fatalf("ship: %d %s", code, stderr)
		}
		var summary string
		for _, c := range fake.Issues[2].Comments {
			summary += c.Body
		}
		for _, want := range []string{"## Shipped", "#7", "Add export", "AC-1: met", "keep it small"} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary lacks %q: %s", want, summary)
			}
		}
		before := len(fake.Issues[2].Comments)
		if code, _, stderr := issueRunWith(t, deps, root, "milestone", "status", "M02", "--to", "shipped"); code != 0 {
			t.Fatalf("retry: %d %s", code, stderr)
		}
		if len(fake.Issues[2].Comments) != before {
			t.Fatal("retry added another closing record")
		}
	})
}
