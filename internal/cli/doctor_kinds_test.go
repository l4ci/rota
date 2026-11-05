package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/projects"
)

func TestDoctorCodexReadinessResolvedHost(t *testing.T) {
	for _, tc := range []struct {
		name, dispatch, registry string
		herdrEnv                 string
		host                     host.Kind
		status                   string
	}{
		{"automatic herdr", "subagent", "", "1", host.Herdr, doctor.Fail},
		{"registry herdr overrides tmux", "tmux", "herdr", "", host.Herdr, doctor.Fail},
		{"registry tmux overrides herdr", "herdr", "tmux", "1", host.Tmux, doctor.Pass},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := doctorDeps(t, map[string]string{
				"git":   "exit 1",
				"tmux":  "exit 0",
				"codex": `case "$1" in --version) echo 'codex-cli 0.159.2';; login) exit 0;; *) exit 1;; esac`,
				"herdr": `case "$1" in --version) echo 'herdr 0.9.3';; integration) echo 'codex: not installed (/x)';; *) exit 1;; esac`,
			})
			root := t.TempDir()
			state := filepath.Join(root, ".rota")
			if err := os.MkdirAll(state, 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := `{"round":{"dispatch":"` + tc.dispatch + `","accounts":[{"kind":"codex","name":"c","dir":"/account"}]}}`
			if err := os.WriteFile(filepath.Join(state, "config.json"), []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.registry != "" {
				if err := os.WriteFile(filepath.Join(state, "workers.json"), []byte(`{"host":"`+tc.registry+`","slots":[]}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			d.Getwd = func() (string, error) { return root, nil }
			d.Getenv = func(k string) string {
				if k == "HERDR_ENV" {
					return tc.herdrEnv
				}
				return ""
			}
			in := doctorInput(context.Background(), d)
			if in.Dispatch != tc.dispatch || in.RoundHost != tc.host {
				t.Fatalf("dispatch %q, host %q; want %q, %q", in.Dispatch, in.RoundHost, tc.dispatch, tc.host)
			}
			for _, c := range doctor.Run(context.Background(), in).Checks {
				if c.Name != "codex" {
					continue
				}
				if c.Status != tc.status || strings.Contains(c.Detail, "herdr integration not current") != (tc.status == doctor.Fail) {
					t.Fatalf("Codex readiness: %+v; want %s", c, tc.status)
				}
				return
			}
			t.Fatal("missing Codex check")
		})
	}
}

// Doctor checks a codex account's login in the expanded home, the same path a
// dispatch launches with (#1757).
func TestDoctorKindsExpandsAccountHomes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	doc := `{"round":{"accounts":[{"kind":"codex","name":"c","dir":"~/.codex"}]}}`
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	k := doctorKinds(config.Load(projects.OSEnv(), p), "/home/u")[harness.Codex]
	if len(k.Homes) != 1 || k.Homes[0].Dir != "/home/u/.codex" {
		t.Errorf("homes = %+v", k.Homes)
	}
}
