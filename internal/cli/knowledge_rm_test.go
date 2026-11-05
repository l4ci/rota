package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/knowledge"
)

func TestKnowledgeRm(t *testing.T) {
	dir := knProject(t, false)
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, ".rota", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// Middle bullet: exactly that line goes, every other byte stays.
	code, out, _ := rotaIn(t, dir, "knowledge", "rm", "--topic", "Architecture", "--title", "Beta rule", "--json")
	if d := data(t, out); code != 0 || d["changed"] != true {
		t.Fatalf("rm: %d %s", code, out)
	}
	want := strings.Replace(knFixtureKnowledge, "- **Beta rule** — Prefer files. <!-- 2026-01-02 -->\n", "", 1)
	if k := read("KNOWLEDGE.md"); k != want {
		t.Errorf("KNOWLEDGE.md:\n%s\nwant:\n%s", k, want)
	}
	st := knowledge.Store{Root: dir}
	for _, title := range []string{"Beta rule", "Alpha rule", "Old rule"} {
		_, found, err := st.TierGet(knowledge.Umbrella, "Architecture", title)
		if err != nil || found != (title != "Beta rule") {
			t.Errorf("%s: found=%v err=%v", title, found, err)
		}
	}

	// Last bullet of a section keeps the blank line before the next heading.
	if code, _, _ := rotaIn(t, dir, "knowledge", "rm", "--topic", "Architecture", "--title", "Old rule"); code != 0 {
		t.Fatalf("rm last: exit %d", code)
	}
	want = strings.Replace(want, "- **Old rule** — Retired. <!-- 2026-01-03 -->\n", "", 1)
	if k := read("KNOWLEDGE.md"); k != want {
		t.Errorf("KNOWLEDGE.md:\n%s\nwant:\n%s", k, want)
	}

	// Unknown title or topic: exit 3, nothing written (a rerun is this case).
	before, beforeTier := read("KNOWLEDGE.md"), read("knowledge-tier.json")
	for _, args := range [][]string{
		{"--topic", "Architecture", "--title", "Beta rule"},
		{"--topic", "Nope", "--title", "Alpha rule"},
	} {
		if code, _, _ := rotaIn(t, dir, append([]string{"knowledge", "rm"}, args...)...); code != 3 {
			t.Errorf("%v: exit %d, want 3", args, code)
		}
	}
	if read("KNOWLEDGE.md") != before || read("knowledge-tier.json") != beforeTier {
		t.Error("a refused rm changed a file")
	}

	for _, args := range [][]string{{"--topic", "Build"}, {"--title", "Gamma tip"}} {
		if code, _, _ := rotaIn(t, dir, append([]string{"knowledge", "rm"}, args...)...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}
