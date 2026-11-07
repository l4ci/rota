package hook

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadViewScopes(t *testing.T) {
	root, user := t.TempDir(), t.TempDir()
	writeSettings(t, SettingsPath(ScopeProject, root), `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"rota hook stop `+Marker+`"}]}]}}`)
	writeSettings(t, SettingsPath(ScopeProjectLocal, root), `{nope`)
	v := LoadView(root, []string{user})

	if got := len(v.InScope()); got != 3 {
		t.Fatalf("in scope = %d files, want local, shared and one user file", got)
	}
	if p := v.Project(); len(p) != 2 || p[0].Scope != ScopeProjectLocal || p[1].Scope != ScopeProject {
		t.Fatalf("project = %+v, want local then shared", p)
	}
	wt := v.Worktree()
	if len(wt) != 2 || wt[0].Scope != ScopeProject || wt[1].Scope != ScopeUser {
		t.Fatalf("worktree = %+v, want shared then user, never project-local", wt)
	}
	if !v.Local.Exists || v.Local.Err == nil {
		t.Fatalf("local = %+v, want existing but unreadable", v.Local)
	}
	if !v.Shared.Exists || v.Shared.Events["Stop"] == "" {
		t.Fatalf("shared = %+v, want Stop event read", v.Shared)
	}
	if v.Users[0].Exists {
		t.Fatalf("missing user file reported as existing: %+v", v.Users[0])
	}
}
