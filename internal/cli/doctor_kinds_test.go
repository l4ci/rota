package cli

import (
	"github.com/l4ci/rota/internal/projects"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/harness"
)

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
