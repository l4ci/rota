package tracker

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/projects"
)

// The default connection lookup reads the env it is given, never the
// process: the secret comes from ROTA_<NAME>_TOKEN of the injected env and
// the connection from the config dir that env names.
func TestMachineConnReadsTheInjectedEnv(t *testing.T) {
	dir := t.TempDir()
	if err := connections.Add(dir, connections.Connection{Name: "work", Kind: connections.KindLinear}, false); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"ROTA_CONFIG_DIR": dir, "XDG_DATA_HOME": t.TempDir(), connections.EnvVar("work"): "lin_api_injected"}
	env := projects.Env{Getenv: func(k string) string { return vars[k] }, Home: func() (string, error) { return t.TempDir(), nil }}
	cfg, err := jsonx.Decode([]byte(`{"issues":{"source":"linear","connection":"work","linear":{"team":"ENG"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewIssueTrackerFromConfig(context.Background(), cfg, t.TempDir(), WithMachine(env, nil)); err != nil {
		t.Fatalf("connection through the injected env: %v", err)
	}
	vars[connections.EnvVar("work")] = ""
	_, err = NewIssueTrackerFromConfig(context.Background(), cfg, t.TempDir(), WithMachine(env, nil))
	if err == nil || !strings.Contains(err.Error(), "no secret") {
		t.Errorf("without the injected token: %v, want a no-secret refusal", err)
	}
}
