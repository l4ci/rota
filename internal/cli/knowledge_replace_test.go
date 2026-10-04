package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeReplace(t *testing.T) {
	dir := knProject(t, false)
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, ".rota", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// Body edit: only the one bullet changes.
	code, out, _ := rotaIn(t, dir, "knowledge", "replace", "--topic", "Architecture", "--old", "Prefer files.", "--new", "Prefer dirs.", "--json")
	if d := data(t, out); code != 0 || d["changed"] != true {
		t.Fatalf("replace: %d %s", code, out)
	}
	if k := read("KNOWLEDGE.md"); !strings.Contains(k, "Prefer dirs.") || !strings.Contains(k, "Keep modules small.") || strings.Contains(k, "Prefer files.") {
		t.Errorf("KNOWLEDGE.md:\n%s", k)
	}

	// Same text again: nothing left to replace.
	if code, _, _ := rotaIn(t, dir, "knowledge", "replace", "--topic", "Architecture", "--old", "Prefer files.", "--new", "x"); code != 3 {
		t.Errorf("no match: exit %d, want 3", code)
	}
	// old == new is a no-op, not an error.
	_, out, _ = rotaIn(t, dir, "knowledge", "replace", "--topic", "Architecture", "--old", "Prefer dirs.", "--new", "Prefer dirs.", "--json")
	if data(t, out)["changed"] != false {
		t.Errorf("old==new: %s", out)
	}
	// "rule" sits in three bullets: refused, exit 4, file untouched.
	before := read("KNOWLEDGE.md")
	code, out, _ = rotaIn(t, dir, "knowledge", "replace", "--topic", "Architecture", "--old", "rule", "--new", "x", "--json")
	if code != 4 || data(t, out)["blockedBy"] != "ambiguous" || read("KNOWLEDGE.md") != before {
		t.Errorf("ambiguous: %d %s", code, out)
	}
	if code, _, _ := rotaIn(t, dir, "knowledge", "replace", "--topic", "Nope", "--old", "a", "--new", "b"); code != 3 {
		t.Errorf("missing topic: exit %d, want 3", code)
	}
	for _, args := range [][]string{
		{"--old", "a", "--new", "b"},
		{"--topic", "Build", "--new", "b"},
		{"--topic", "Build", "--old", "a"},
	} {
		if code, _, _ := rotaIn(t, dir, append([]string{"knowledge", "replace"}, args...)...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}

	// A title edit re-keys the tier entry, keeping its hits.
	if code, _, _ := rotaIn(t, dir, "knowledge", "replace", "--topic", "Architecture", "--old", "**Beta rule**", "--new", "**Beta law**"); code != 0 {
		t.Fatalf("retitle: exit %d", code)
	}
	tier := read("knowledge-tier.json")
	if strings.Contains(tier, "Architecture::Beta rule") || !strings.Contains(tier, "Architecture::Beta law") {
		t.Errorf("sidecar not re-keyed:\n%s", tier)
	}
}

func TestMilestoneOverview(t *testing.T) {
	dir := gitRepo(t)
	if code, _, _ := rotaStdin(t, dir, "x", "milestone", "overview", "--body-file", "-"); code != 3 {
		t.Errorf("no MILESTONES.md: exit %d, want 3", code)
	}
	rotaIn(t, dir, "milestone", "add", "--title", "First", "--summary", "S")
	rotaIn(t, dir, "milestone", "status", "M01", "--to", "active")
	path := filepath.Join(dir, ".rota", "MILESTONES.md")
	before, _ := os.ReadFile(path)
	_, tail, _ := strings.Cut(string(before), "\n## ")

	code, out, _ := rotaStdin(t, dir, "New overview.\n\nSecond paragraph.\n", "milestone", "overview", "--body-file", "-", "--json")
	if code != 0 || data(t, out)["changed"] != true {
		t.Fatalf("overview: %d %s", code, out)
	}
	after, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(after), "New overview.\n\nSecond paragraph.\n\n## ") {
		t.Errorf("head:\n%s", after)
	}
	if _, tail2, _ := strings.Cut(string(after), "\n## "); tail2 != tail {
		t.Errorf("sections changed:\n%s\n--- was ---\n%s", tail2, tail)
	}
	_, out, _ = rotaStdin(t, dir, "New overview.\n\nSecond paragraph.", "milestone", "overview", "--body-file", "-", "--json")
	if data(t, out)["changed"] != false {
		t.Errorf("idempotent: %s", out)
	}
	if code, _, _ := rotaStdin(t, dir, "x\n## Sneaky\n", "milestone", "overview", "--body-file", "-"); code != 4 {
		t.Errorf("heading in body: exit %d, want 4", code)
	}
	if code, _, _ := rotaStdin(t, dir, "  \n", "milestone", "overview", "--body-file", "-"); code != 2 {
		t.Errorf("empty body: exit %d, want 2", code)
	}
	// With a title line, the title stays and the overview follows it.
	os.WriteFile(path, []byte("# Milestones\n\nold wording\n\n## Active milestones\n\n_(none)_\n"), 0o666)
	rotaStdin(t, dir, "fresh wording", "milestone", "overview", "--body-file", "-")
	if b, _ := os.ReadFile(path); string(b) != "# Milestones\n\nfresh wording\n\n## Active milestones\n\n_(none)_\n" {
		t.Errorf("titled:\n%s", b)
	}
	// index keeps the overview.
	rotaIn(t, dir, "milestone", "index")
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "fresh wording") {
		t.Errorf("index dropped the overview:\n%s", b)
	}
}
