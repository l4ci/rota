package backlog

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func TestExtractTokens(t *testing.T) {
	cases := []struct {
		title        string
		dist, common []string
	}{
		{"Add `parser-core` and lexer-v2 support", []string{"parser-core", "lexer-v2"}, nil},
		{"Handle Quoting in Parser", nil, []string{"handle", "quoting", "parser"}},
		{"fix the new bug", nil, nil},
		{"update src/main.go and src/main.go", []string{"src/main.go"}, nil},
		{"Wide `a b` span", []string{"a b"}, []string{"wide", "span"}},
	}
	for _, c := range cases {
		d, co := extractTokens(c.title)
		if !reflect.DeepEqual(d, c.dist) || !reflect.DeepEqual(co, c.common) {
			t.Errorf("%q: got %v / %v, want %v / %v", c.title, d, co, c.dist, c.common)
		}
	}
}

func TestPathExists(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "src"), 0o755)
	os.WriteFile(filepath.Join(root, "src", "a.go"), nil, 0o644)
	for tok, want := range map[string]string{
		"src/a.go": "src/a.go", "/src//./a.go": "src/a.go", "`src/a.go`": "src/a.go", "src/": "src",
		"src/none.go": "", "plain": "", "src/a b.go": "", "./": ".",
	} {
		if got := pathExists(tok, root); got != want {
			t.Errorf("pathExists(%q) = %q, want %q", tok, got, want)
		}
	}
}

func TestAudit(t *testing.T) {
	f, _ := proj(t, nil)
	gittest.Write(t, f.Root, "lexer.go", "")
	gittest.Run(t, f.Root, "add", "lexer.go")
	for i, msg := range []string{"feat: add `parser-core` lexer-v2 one", "feat: add `parser-core` lexer-v2 two", "feat: rework tokenizer pass"} {
		gittest.Commit(t, f.Root, msg, "x"+string(rune('a'+i)), "")
	}
	got := Audit(f.Root, []string{"Add `parser-core` for lexer-v2", "   ", "Sharpen the tokenizer pass", "`lexer.go` rewrite", "Nothing here zzzz"})
	if len(got) != 4 {
		t.Fatalf("blank titles are skipped, got %d audits", len(got))
	}
	strong := got[0].Hits
	if len(strong) != 2 || strong[0].Level != HitStrong || strong[0].Subject != "feat: add `parser-core` lexer-v2 two" ||
		!reflect.DeepEqual(strong[0].Tokens, []string{"parser-core", "lexer-v2"}) {
		t.Errorf("strong hits = %+v", strong)
	}
	if h := got[1].Hits; len(h) != 1 || h[0].Level != HitMedium || !strings.HasPrefix(h[0].Subject, "feat: rework") {
		t.Errorf("medium hits = %+v", h)
	}
	if h := got[2].Hits; len(h) != 1 || h[0].Level != HitPath || h[0].Path != "lexer.go" {
		t.Errorf("path hits = %+v", h)
	}
	if len(got[3].Hits) != 0 {
		t.Errorf("no hits expected, got %+v", got[3].Hits)
	}
}
