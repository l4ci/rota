package postgate

import (
	"os"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	_ "github.com/l4ci/rota/internal/testiso" // tests never read the real global config
)

func TestMain(m *testing.M) {
	gittest.SetIdentity()
	os.Exit(m.Run())
}
