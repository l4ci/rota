package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookPathScopedRecall(t *testing.T) {
	dir := orchProject(t, false)
	knWrite(t, filepath.Join(dir, ".rota", "KNOWLEDGE.md"), `# Knowledge
## Architecture
- **Scoped tip** — Keep the seam. <!-- paths: ["internal/*.go", "cmd/*.go"] --> <!-- 2026-01-01 -->
- **Unscoped tip** — Topic only. <!-- 2026-01-01 -->
`)
	knWrite(t, filepath.Join(dir, ".rota", "DECISIONS.md"), `# Decisions
## Architecture
### Scoped boundary
Keep storage private.
<!-- paths: ["internal/*.go"] -->
**Forbids.** Public storage.
**Permits.** Private helpers.
### Retired boundary
<!-- paths: ["internal/*.go"] -->
*Status.* Retired 2026-01-01.
`)
	call := func(session, tool, file string) string {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"session_id": session, "cwd": dir, "tool_name": tool, "tool_input": map[string]string{"file_path": file}})
		code, out, errOut := rotaStdin(t, dir, string(b), "hook", "guard")
		if code != 0 || errOut != "" {
			t.Fatalf("hook: %d %s", code, errOut)
		}
		return out
	}
	if out := call("recall-1", "Edit", "other.txt"); out != "" {
		t.Fatalf("unmatched: %s", out)
	}
	out := call("recall-1", "Edit", filepath.Join(dir, "internal", "x.go"))
	repeated := call("recall-1", "Write", "internal/y.go")
	newSession := call("recall-2", "Write", "internal/y.go")
	t.Logf("Scoped tip injections: first=%d repeat=%d new-session=%d", strings.Count(out, "Scoped tip"), strings.Count(repeated, "Scoped tip"), strings.Count(newSession, "Scoped tip"))
	if !strings.Contains(out, "Scoped tip") || !strings.Contains(out, "Scoped boundary") || !strings.Contains(out, "Public storage") {
		t.Fatalf("matching edit did not inject complete entries: %q", out)
	}
	if strings.Contains(out, "Unscoped tip") || strings.Contains(out, "Retired boundary") {
		t.Fatalf("unexpected entry: %s", out)
	}
	var payload struct {
		Output struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil || payload.Output.Event != "PreToolUse" || payload.Output.Context == "" {
		t.Fatalf("invalid hook output: %s", out)
	}
	if out := repeated; out != "" {
		t.Fatalf("duplicate: %s", out)
	}
	if out := newSession; !strings.Contains(out, "Scoped tip") {
		t.Fatalf("new session: %q", out)
	}
}
