package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func installAt(t *testing.T, root, mode string) {
	t.Helper()
	s, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install([]Root{{Path: root}}, Options{Version: "t", Mode: mode}); err != nil {
		t.Fatal(err)
	}
}

// The installed contract comes from the first rota-installed root: project
// before user, Claude before Codex. A stub root has a manifest but no
// references, so it yields "" for the caller to fall back to the cache.
func TestInstalledBrief(t *testing.T) {
	top, home := t.TempDir(), t.TempDir()
	env := fakeEnv(map[string]string{"HOME": home})
	rel := filepath.Join("rota-orchestrate", "references", "worker-contract.md")

	if p := InstalledBrief(env, top); p != "" {
		t.Fatalf("nothing installed: %q", p)
	}
	userRoot := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(filepath.Join(userRoot, filepath.Dir(rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userRoot, rel), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := InstalledBrief(env, top); p != "" {
		t.Errorf("a root without a manifest counts as installed: %q", p)
	}
	installAt(t, userRoot, ModeFull)
	if p := InstalledBrief(env, top); p != filepath.Join(userRoot, rel) {
		t.Errorf("full user root: %q", p)
	}
	codex := filepath.Join(home, ".agents", "skills")
	installAt(t, codex, ModeFull)
	if p := InstalledBrief(env, top); p != filepath.Join(userRoot, rel) {
		t.Errorf("Claude before Codex: %q", p)
	}
	proj := filepath.Join(top, ".claude", "skills")
	installAt(t, proj, ModeFull)
	if p := InstalledBrief(env, top); p != filepath.Join(proj, rel) {
		t.Errorf("project before user: %q", p)
	}
	installAt(t, proj, ModeStub)
	if p := InstalledBrief(env, top); p != "" {
		t.Errorf("stub root must yield no brief, got %q", p)
	}
}

func TestInstalledBriefCodexOnly(t *testing.T) {
	top, home := t.TempDir(), t.TempDir()
	root := filepath.Join(top, ".agents", "skills")
	installAt(t, root, ModeFull)
	want := filepath.Join(root, "rota-orchestrate", "references", "worker-contract.md")
	if p := InstalledBrief(fakeEnv(map[string]string{"HOME": home}), top); p != want {
		t.Errorf("project codex root: %q", p)
	}
}

func TestCheckoutBrief(t *testing.T) {
	top := t.TempDir()
	if p := CheckoutBrief(top); p != "" {
		t.Fatalf("empty checkout: %q", p)
	}
	want := filepath.Join(top, "skills", "references", "worker-contract.md")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p := CheckoutBrief(top); p != want {
		t.Errorf("got %q", p)
	}
}

func TestCachedBrief(t *testing.T) {
	if p := CachedBrief("t", ""); p != "" {
		t.Errorf("empty cache must skip: %q", p)
	}
	cache := t.TempDir()
	p := CachedBrief("t", cache)
	if p == "" || filepath.Base(p) != "worker-contract.md" {
		t.Fatalf("no cached brief: %q", p)
	}
	if p2 := CachedBrief("t", cache); p2 != p {
		t.Errorf("second lookup moved: %q vs %q", p2, p)
	}
}
