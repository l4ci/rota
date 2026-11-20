package issues

import (
	"fmt"
	"os"
	"testing"

	"github.com/l4ci/rota/internal/testiso"
)

// TestMain puts tripwire gh and glab first on PATH: every test injects its
// executor, so any real forge CLI call fails the run.
func TestMain(m *testing.M) {
	tw, err := testiso.NewTripwire("issues-tripwire", "gh", "glab")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	if hit := tw.Check(); hit != "" {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd the forge CLI from PATH:\n%s", hit)
		code = 1
	}
	tw.Close()
	os.Exit(code)
}
