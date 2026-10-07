package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func agentsProject(t *testing.T, cfg string) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o755)
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(cfg), 0o644)
	return dir
}

func TestAgentsWriteAndCheck(t *testing.T) {
	dir := agentsProject(t, `{}`)
	if code, _, _ := rotaIn(t, dir, "agents", "write", "--check"); code != 1 {
		t.Fatalf("check before write: %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); err == nil {
		t.Fatal("--check wrote")
	}
	code, out, errOut := rotaIn(t, dir, "agents", "write")
	if code != 0 || !strings.Contains(out, "created: .claude/agents/rota-explorer.md") {
		t.Fatalf("write: %d %s %s", code, out, errOut)
	}
	if code, out, _ := rotaIn(t, dir, "agents", "write", "--check"); code != 0 || strings.Contains(out, "missing") {
		t.Fatalf("check after write: %d %s", code, out)
	}
	if code, out, _ := rotaIn(t, dir, "agents", "write"); code != 0 || strings.Contains(out, "created") || strings.Contains(out, "updated") {
		t.Fatalf("second write not a no-op: %d %s", code, out)
	}
	// Config drift: check fails and still writes nothing.
	os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"roles":{"reasoner":{"effort":"high"}}}`), 0o644)
	if code, _, _ := rotaIn(t, dir, "agents", "write", "--check"); code != 1 {
		t.Fatalf("drift check: %d", code)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".claude/agents/rota-reasoner.md"))
	if strings.Contains(string(b), "effort") {
		t.Fatal("--check wrote")
	}
}

func TestAgentsWriteRefusals(t *testing.T) {
	dir := agentsProject(t, `{}`)
	p := filepath.Join(dir, ".claude/agents/rota-explorer.md")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("hand written\n"), 0o644)
	if code, _, errOut := rotaIn(t, dir, "agents", "write"); code != 4 || !strings.Contains(errOut, "rota-explorer.md") {
		t.Fatalf("foreign: %d %s", code, errOut)
	}
	if b, _ := os.ReadFile(p); string(b) != "hand written\n" {
		t.Fatal("foreign file overwritten")
	}

	dir = agentsProject(t, `{"work":{"codexCommand":"codex"},"roles":{"reasoner":{"effort":"max"}}}`)
	if code, _, errOut := rotaIn(t, dir, "agents", "write"); code != 4 || !strings.Contains(errOut, "roles.reasoner.effort") {
		t.Fatalf("codex effort: %d %s", code, errOut)
	}
}

func TestAgentsBadRoleConfigIsAnError(t *testing.T) {
	dir := agentsProject(t, `{"roles":{"explorer":{"tier":"giant"}}}`)
	if code, _, errOut := rotaIn(t, dir, "agents", "write"); code == 0 || !strings.Contains(errOut, "roles.explorer.tier") {
		t.Fatalf("%d %s", code, errOut)
	}
}
