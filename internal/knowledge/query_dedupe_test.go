package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueryDuplicateTopicsPrintOnceInEveryScope(t *testing.T) {
	s := lockedStore(t)
	s.Repos = map[string]string{"web": s.Root + "/web"}
	if err := os.MkdirAll(filepath.Join(s.Root, ".rota", "knowledge", "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := "## Arch\n\n- **local** — sub body <!-- 2026-01-01 -->\n"
	if err := os.WriteFile(filepath.Join(s.Root, ".rota", "knowledge", "web", "KNOWLEDGE.md"), []byte(sub), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{Umbrella, "web"} {
		out, missing, err := s.Query(scope, []string{"arch", "Arch ", "arch", "nope", "NOPE", " "}, QueryOpts{})
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(out, "## Arch\n"); n != 1 {
			t.Fatalf("scope %q: section printed %d times:\n%s", scope, n, out)
		}
		if len(missing) != 1 || missing[0] != "nope" {
			t.Fatalf("scope %q: missing = %q", scope, missing)
		}
	}
}
