package ship

import (
	"os"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// TestMain gives the git the code under test runs (merge commits) an identity.
func TestMain(m *testing.M) {
	gittest.SetIdentity()
	os.Exit(m.Run())
}
