package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A decisions write touches only the entry it was asked to change: the
// preamble, legacy `/hv-` names included, stays byte-identical.
func TestAddDecisionLeavesPreambleByteIdentical(t *testing.T) {
	s := Store{Root: t.TempDir()}
	path := filepath.Join(s.Root, ".rota", "DECISIONS.md")
	preamble := "# Decisions\n\nRecord with `/hv-decide`, not by hand.\n\n"
	before := preamble + "## Architecture\n\n### Live\n\nKeep boundaries.\n\n*Why.* Prevent drift.\n\n**Forbids.** Hand edits.\n\n**Permits.** Verbs.\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddDecision("Architecture", "Another", "Keep boundaries.\n\n*Why.* Prevent drift.\n\n**Forbids.** Hand edits.\n\n**Permits.** Verbs.", "2026-10-27"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), preamble) {
		t.Fatalf("preamble rewritten:\n%s", b)
	}
}
