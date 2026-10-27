package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/rotatree"
)

// Runtime config reads stay in internal/config and cli/deps.go; every
// verb and leaf package takes it from Deps.Config (a loaded value or a loader
// func), so a test can swap in an in-memory config.
func TestConfigLoadsStayOnDeps(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
			// The standalone reference generator has no invocation Deps. Its explicit
			// --config input uses the canonical loader; runtime consumers stay guarded.
			if strings.HasPrefix(rel, "internal/config/") || rel == "internal/cli/deps.go" || rel == "internal/labels/genref/main.go" {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				if strings.Contains(line, "config.Load(") {
					t.Errorf("%s calls config.Load; take the config from Deps.Config: %s", rel, strings.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// `ship strategy` runs against an in-memory config: the project has no
// .rota/config.json, so only the swapped Deps.Config can name the strategy.
func TestShipStrategyReadsInMemoryConfig(t *testing.T) {
	root := trackerProject(t, "")
	if _, err := os.Stat(filepath.Join(root, ".rota", "config.json")); err == nil {
		t.Fatal("fixture wrote a config.json")
	}
	d := originDeps("git@github.com:o/r.git")
	d.Config = func(string) any {
		cfg, err := jsonx.Decode([]byte(`{"issues":{"source":"file"},"work":{"mergeStrategy":"direct"}}`))
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	code, env, stderr := rotaRunWith(t, d, "--json", "-C", root, "ship", "strategy")
	if code != 0 {
		t.Fatalf("exit %d: %v %s", code, env, stderr)
	}
	if got := get(dataOf(env), "strategy"); got != "direct" {
		t.Errorf("strategy = %q, want direct from the in-memory config: %v", got, env)
	}
}

// diskConfig reads the .rota/config.json a test wrote, as the real Deps.Config does.
func diskConfig(root string) any { return config.Load(rotatree.Config(root)) }
