package tracker

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/l4ci/rota/internal/testiso"
)

// tripwire holds the fake gh and glab on PATH.
var tripwire *testiso.Tripwire

// TestMain puts tripwire gh and glab first on PATH, so a test that forgets to
// inject its executor fails instead of reaching a real forge.
func TestMain(m *testing.M) {
	tw, err := testiso.NewTripwire("tracker-tripwire", "gh", "glab")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tripwire = tw
	code := m.Run()
	if hit := tw.Check(); hit != "" {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd the forge CLI from PATH:\n%s", hit)
		code = 1
	}
	tw.Close()
	os.Exit(code)
}

func TestTripwireCatchesRealCLI(t *testing.T) {
	r, err := (&CLI{Provider: "github"}).Run(context.Background(), []string{"issue", "list"}, nil)
	if err != nil || r.ExitCode != 99 {
		t.Fatalf("default executor did not hit the tripwire: %+v, %v", r, err)
	}
	// This test hits the tripwire on purpose; clear the record of it.
	os.Remove(tripwire.Hit)
}
