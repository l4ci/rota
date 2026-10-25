package cli

import (
	"testing"

	"github.com/l4ci/rota/internal/version"
)

// TestUpdateEnvPassesRawLinkTimeCommit pins the deps.go wiring behind #1906:
// version.Get shortens the commit, which hides a checkout build, so UpdateEnv
// must hand update.detect the raw link-time value.
func TestUpdateEnvPassesRawLinkTimeCommit(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef01234567"
	old := version.Commit
	version.Commit = full
	t.Cleanup(func() { version.Commit = old })
	if got := defaultDeps().UpdateEnv().Commit; got != full {
		t.Errorf("UpdateEnv().Commit = %q, want the raw %q", got, full)
	}
}
