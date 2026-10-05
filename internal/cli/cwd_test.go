package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// From a symlinked cwd with $PWD naming the link (what a shell leaves after
// `cd link/src/deep`), the root walk and the sub-repo match use the physical
// path, as Python's os.getcwd() does (acceptance, section 15).
func TestSymlinkedCwdResolvesPhysically(t *testing.T) {
	base := t.TempDir()
	umb := filepath.Join(base, "umb")
	deep := filepath.Join(umb, "web", "src", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(umb, ".rota"), 0o755)
	os.WriteFile(filepath.Join(umb, ".rota", "repos.json"), []byte(`{"repos": [{"name": "web", "path": "web"}]}`), 0o644)
	link := filepath.Join(base, "link")
	if err := os.Symlink(filepath.Join(umb, "web"), link); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	via := filepath.Join(link, "src", "deep")
	if err := os.Chdir(via); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PWD", via)
	code, env, stderr := rotaRun(t, "--json", "repo", "umbrella")
	if code != 0 || get(dataOf(env), "umbrella") != true {
		t.Fatalf("repo umbrella from %s: exit %d, %v, %s", via, code, env, stderr)
	}
}
