package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

// Only deps.go reads the project config from disk; every verb takes it from
// Deps.Config, so a test can swap in an in-memory config.
func TestCliConfigLoadsStayOnDeps(t *testing.T) {
	for _, f := range cliSources(t) {
		if strings.HasSuffix(f, "_test.go") || f == "deps.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if strings.Contains(line, "config.Load(") {
				t.Errorf("%s calls config.Load; read the config through Deps.Config: %s", f, strings.TrimSpace(line))
			}
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
