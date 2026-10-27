package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/roundwatch"
)

func seedFreeze(t *testing.T, root string) string {
	t.Helper()
	p := filepath.Join(root, ".git", "rota", "freeze.json")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"active":true,"base":"feat/x","sha":"red","prs":["https://github.com/o/r/pull/7"],"checks":["false"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRoundFreezeStatusRepairAndAuditedClear(t *testing.T) {
	root, deps := roundFixture(t, nil)
	p := seedFreeze(t, root)
	code, out, _ := rotaInWith(t, deps, root, "--json", "round", "status")
	if code != 0 || data(t, out)["freeze"] == nil {
		t.Fatalf("status must show freeze: %d %s", code, out)
	}
	if line := roundwatch.Digest(root, 40, true); !strings.Contains(line, "FROZEN") {
		t.Fatalf("prompt digest must show freeze: %s", line)
	}
	code, out, _ = rotaInWith(t, deps, root, "--json", "round", "freeze", "--repair-issue", "99")
	if code != 0 {
		t.Fatalf("designate repair: %d %s", code, out)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"repairIssue": "99"`) {
		t.Fatalf("repair not persisted: %s", b)
	}
	code, _, _ = rotaInWith(t, deps, root, "round", "freeze", "--clear")
	if code != 2 {
		t.Fatalf("clear needs a reason: %d", code)
	}
	code, out, _ = rotaInWith(t, deps, root, "--json", "round", "freeze", "--clear", "--reason", "red check is obsolete")
	if code != 0 {
		t.Fatalf("manual clear: %d %s", code, out)
	}
	b, _ = os.ReadFile(p)
	var f map[string]any
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	if f["active"] != false || f["clearedBy"] == "" || f["clearedSHA"] == "" {
		t.Fatalf("clear provenance: %s", b)
	}
	b, err := os.ReadFile(filepath.Join(root, ".rota", "gate-audit.jsonl"))
	if err != nil || !strings.Contains(string(b), "red check is obsolete") || !strings.Contains(string(b), "freeze-clear") {
		t.Fatalf("clear needs audit: %s %v", b, err)
	}
}

func TestRoundFreezeHookPromptWithoutActiveSlots(t *testing.T) {
	root := orchProject(t, true)
	seedFreeze(t, root)
	out := promptOut(t, root)
	if !strings.Contains(out, "FROZEN") {
		t.Fatalf("prompt must show active freeze even without active slots: %q", out)
	}
}

// Logs the measured refusal exits so the same fixture can be run on the base.
func TestRoundFreezeRefusalExitCodes(t *testing.T) {
	for _, verb := range []string{"gate", "train", "assign"} {
		t.Run(verb, func(t *testing.T) {
			var code int
			var out string
			if verb == "assign" {
				f := newScopeFx(t, "warn", "")
				seedFreeze(t, f.root)
				code, out, _ = rotaInWith(t, f.deps, f.root, "--json", "round", "assign", "2", "--agent", "dana", "--check-only", "--holder-pid", "4242")
			} else {
				root := workerProject(t, `{"forge":{"provider":"local"},"ship":{"review":"none"},"test":{"full":["test -f feature.txt"]}}`)
				if rc, out, _ := rotaIn(t, root, "worker", "pool", "init", "--slots", "1", "--base", "main"); rc != 0 {
					t.Fatalf("init: %d %s", rc, out)
				}
				wt := filepath.Join(root, ".worktrees", "w1")
				write(t, filepath.Join(wt, "feature.txt"), "f")
				gitT(t, wt, "add", "feature.txt")
				gitT(t, wt, "commit", "-q", "-m", "feature")
				p := seedFreeze(t, root)
				b, _ := os.ReadFile(p)
				os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "feat/x", "main")), 0644)
				code, out, _ = rotaIn(t, root, "--json", "worker", verb, "w1", "--base", "main", "--no-assign-check")
			}
			d := data(t, out)
			t.Logf("freeze refusal %s: exit=%d blockedBy=%v", verb, code, d["blockedBy"])
			if code != 4 || d["blockedBy"] != "freeze" {
				t.Errorf("frozen %s: exit=%d data=%v", verb, code, d)
			}
		})
	}
}
