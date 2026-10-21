package connections

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/projects"
)

type stubKeychain struct{ Store }

func TestMachineKeychainIsOptional(t *testing.T) {
	vars := map[string]string{"ROTA_CONFIG_DIR": t.TempDir(), "XDG_DATA_HOME": t.TempDir()}
	env := projects.Env{Getenv: func(k string) string { return vars[k] }, Home: func() (string, error) { return t.TempDir(), nil }}
	for name, kc := range map[string]func() Store{
		"no func":   nil,
		"nil store": func() Store { return nil },
		"one store": func() Store { return stubKeychain{} },
	} {
		_, sec, err := Machine(env, kc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := sec.Keychain != nil; got != (name == "one store") {
			t.Errorf("%s: keychain set = %v", name, got)
		}
	}
	_, sec, _ := Machine(env, nil)
	vars[EnvVar("x")] = "tok"
	if got, _, err := sec.Resolve(context.Background(), "x"); err != nil || got != "tok" {
		t.Errorf("env override: %q %v", got, err)
	}
}
