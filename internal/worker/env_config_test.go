package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/cfgdir"
	"github.com/l4ci/rota/internal/config"
)

// Config is required: an Env that sets none is refused, never quietly given
// the schema defaults. NoConfig is the explicit way to ask for them.
func TestWithDefaultsRefusesNilConfig(t *testing.T) {
	defer func() {
		r := recover()
		msg, _ := r.(string)
		if !strings.Contains(msg, "Config is required") {
			t.Fatalf("recovered %v, want a panic naming the required Config", r)
		}
	}()
	Env{}.WithDefaults()
}

func TestWithDefaultsKeepsExplicitConfig(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".rota")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"git":{"remote":"from-disk"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := (Env{Config: NoConfig}).WithDefaults().Config(root); got != nil {
		t.Fatalf("NoConfig = %v, want nil", got)
	}
	if got, _ := config.GitRemote((Env{Config: diskConfig}).WithDefaults().Config(root)); got != "from-disk" {
		t.Fatalf("diskConfig git.remote = %q, want from-disk", got)
	}
}

// diskConfig is the Config of a test Env that reads the .rota/config.json the
// test wrote, as cli's Deps.Config does in production. A test Env sets it
// explicitly; a bare Env{} is refused.
func diskConfig(root string) any { return config.Load(cfgdir.OSEnv(), root) }
