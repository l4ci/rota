package worker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/config"
)

// An Env with no Config sees no config at all: the worker package reads no
// config file on its own, so a test that needs one hands it in (diskConfig).
func TestWithDefaultsConfigIsEmptyNotDisk(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".rota")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"git":{"remote":"from-disk"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (Env{}).WithDefaults().Config(root); got != nil {
		t.Fatalf("default Config = %v, want nil (no disk read)", got)
	}
	if got, _ := config.GitRemote((Env{Config: diskConfig}).WithDefaults().Config(root)); got != "from-disk" {
		t.Fatalf("diskConfig git.remote = %q, want from-disk", got)
	}
}

// diskConfig is the Config of a test Env that reads the .rota/config.json the
// test wrote, as cli's Deps.Config does in production. A test Env sets it
// explicitly; a bare Env{} sees no config.
func diskConfig(root string) any { return config.Load(cfgdir.OSEnv(), root) }
