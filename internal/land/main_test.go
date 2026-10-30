package land

import (
	"os"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func TestMain(m *testing.M) {
	// The merges under test commit: the identity comes from here, not from the
	// developer's (or CI's missing) global git config.
	gittest.SetIdentity()
	os.Exit(m.Run())
}
