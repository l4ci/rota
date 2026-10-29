package roundtick

import (
	"testing"

	"github.com/l4ci/rota/internal/fsio/fsiotest"
)

func TestUpdateWritesJsonxBytes(t *testing.T) {
	dir := t.TempDir()
	err := update(dir, 1, func(s *State) { s.Held = map[string]string{"k": fsiotest.Hostile} })
	if err != nil {
		t.Fatal(err)
	}
	fsiotest.RequireJsonxBytes(t, statePath(dir))
}
