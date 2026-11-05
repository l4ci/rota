package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeLegacyTierAmbiguityIsScoped(t *testing.T) {
	dir := knProject(t, false)
	knWrite(t, filepath.Join(dir, ".rota", "KNOWLEDGE.md"), "## A::B\n\n- **C** — body <!-- 2026-01-01 -->\n\n## Safe\n\n- **Known** — body <!-- 2026-01-01 -->\n")
	p := filepath.Join(dir, ".rota", "knowledge-tier.json")
	raw := `{"version":1,"entries":{"A::B::C":{"tier":"deprecated","hits":7},"Safe::Known":{"tier":"confirmed","hits":2}}}`
	knWrite(t, p, raw)
	got := knNew(t, dir, "", "knowledge", "query", "Safe")
	if got.rc != 0 || !strings.Contains(got.stdout, "**Known**") {
		t.Fatalf("unrelated query: %+v", got)
	}
	got = knNew(t, dir, "", "knowledge", "query", "A::B")
	if got.rc != 4 {
		t.Fatalf("ambiguous query: rc=%d stderr=%s", got.rc, got.stderr)
	}
	for _, want := range []string{`A::B::C`, `("A", "B::C")`, `("A::B", "C")`, "repair"} {
		if !strings.Contains(got.stderr, want) {
			t.Errorf("diagnostic %q omits %q", got.stderr, want)
		}
	}
	if b, err := os.ReadFile(p); err != nil || string(b) != raw {
		t.Fatalf("refused query changed sidecar: %s err=%v", b, err)
	}
}
