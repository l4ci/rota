package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathEntriesScopeAndLifecycle(t *testing.T) {
	root := t.TempDir()
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(".rota/KNOWLEDGE.md", "# K\n## Architecture\n- **Umbrella** — Shared. <!-- paths: [\"web/**/*.go\"] --> <!-- 2026-01-01 -->\n- **Old** — Stale. <!-- paths: [\"**\"] --> <!-- 2026-01-01 -->\n")
	write(".rota/knowledge-tier.json", `{"version":1,"entries":{"Architecture::Old":{"tier":"deprecated"}}}`)
	write(".rota/knowledge/web/KNOWLEDGE.md", "# K\n## Architecture\n- **Local** — UI seam.\n  Keep the continuation. <!-- paths: [\"src/*.go\"] -->\n")
	write(".rota/knowledge/api/KNOWLEDGE.md", "# K\n## Architecture\n- **Wrong repo** — API. <!-- paths: [\"**\"] -->\n")
	s := Store{Root: root, Repos: map[string]string{"web": filepath.Join(root, "web"), "api": filepath.Join(root, "api")}}
	entries, err := s.PathEntries(filepath.Join(root, "web/src/a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !strings.Contains(entries[0].Text, "Umbrella") || !strings.Contains(entries[1].Text, "Keep the continuation") {
		t.Fatalf("entries: %+v", entries)
	}
	if entries, err := s.PathEntries(filepath.Join(root, "../outside.go")); err != nil || len(entries) != 0 {
		t.Fatalf("outside: %+v %v", entries, err)
	}
	for _, p := range []string{".rota/knowledge/web/knowledge-tier.json", ".rota/knowledge/api/knowledge-tier.json"} {
		if _, err := os.Stat(filepath.Join(root, p)); !os.IsNotExist(err) {
			t.Fatalf("read created %s", p)
		}
	}
}

func TestScopedMatchGlobsAndInvalidFields(t *testing.T) {
	for _, tc := range []struct {
		field, file string
		want        bool
	}{
		{`<!-- paths: ["internal/**/*.go"] -->`, "internal/x.go", true},
		{`<!-- paths: ["internal/**/*.go"] -->`, "internal/a/b/x.go", true},
		{`<!-- paths: ["internal/*.go"] -->`, "internal/a/x.go", false},
		{`<!-- paths: ["cmd/?.[ch]", "web/*.go"] -->`, "web/x.go", true},
		{`<!-- paths: ["["] -->`, "x.go", false},
		{`<!-- paths: ["../**"] -->`, "../x.go", false},
		{`<!-- paths: broken -->`, "x.go", false},
		{"unscoped", "x.go", false},
	} {
		if got := scopedMatch(tc.field, tc.file); got != tc.want {
			t.Errorf("%s %s: %v", tc.field, tc.file, got)
		}
	}
}
