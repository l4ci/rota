package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/config"
)

// doctorKinds reads each kind's tier map through the shared harness seam, so
// a set tier marks the kind and a blank one does not (#890).
func TestDoctorKindsTiersFollowTheTierMap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	doc := `{"round":{"tiers":{"codex":{"heavy":"m"},"opencode":{"light":"  "}}}}`
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	kinds := doctorKinds(config.Load(p))
	if in, ok := kinds["codex"]; !ok || !in.Tiers {
		t.Errorf("codex has a tier set, doctorKinds reports %+v (present %v)", in, ok)
	}
	if in, ok := kinds["opencode"]; !ok || in.Tiers {
		t.Errorf("opencode tier is blank, doctorKinds reports %+v (present %v)", in, ok)
	}
}
