package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundAssignDecisionTopics(t *testing.T) {
	f := newScopeFx(t, "warn", "")
	write(t, filepath.Join(f.root, ".rota", "DECISIONS.md"), "# Decisions\n\n## Architecture\n\n### Shared store\n\nKeep the store.\n\n## Rounds\n\n### Claims\n\nKeep claims.\n")
	args := []string{"--json", "round", "assign", "2", "--agent", "dana", "--holder-pid", "4242", "--decision-topics", "Architecture,Rounds"}
	code, out, errOut := rotaInWith(t, f.deps, f.root, args...)
	if code != 0 {
		t.Fatalf("assign exit %d, want 0: %s %s", code, out, errOut)
	}
	raw, err := os.ReadFile(filepath.Join(f.root, ".rota", "workers.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Keep the store.", "Keep claims."} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("persisted assignment lacks %q: %s", want, raw)
		}
	}
}

func TestRoundAssignUnknownDecisionTopicDoesNotClaim(t *testing.T) {
	f := newScopeFx(t, "warn", "")
	write(t, filepath.Join(f.root, ".rota", "DECISIONS.md"), "# Decisions\n\n## Architecture\n\n### Shared store\n\nKeep the store.\n")
	before, err := os.ReadFile(filepath.Join(f.root, ".rota", "workers.json"))
	if err != nil {
		t.Fatal(err)
	}
	code, out, errOut := rotaInWith(t, f.deps, f.root, "--json", "round", "assign", "2", "--agent", "dana", "--holder-pid", "4242", "--decision-topics", "Architectuer")
	if code != 2 || !strings.Contains(out+errOut, "unknown decision topics: Architectuer") {
		t.Fatalf("unknown topic exit %d: %s %s", code, out, errOut)
	}
	after, err := os.ReadFile(filepath.Join(f.root, ".rota", "workers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("refused topic changed registry: %s", after)
	}
}
