package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

// Round start must word a risky-but-unbaselined repo config the way doctor
// does: "no baseline", not "changed since the baseline".
func TestRoundStartRepoConfigWarnsNoBaseline(t *testing.T) {
	dir := t.TempDir()
	gittest.Run(t, dir, "init", "-q")
	gittest.Run(t, dir, "config", "--local", "core.hooksPath", "/x")
	var stderr bytes.Buffer
	c := &Ctx{Path: "rota round start", Deps: testDeps(), Stderr: &stderr, ctx: context.Background()}
	warnRepoConfigDrift(c, context.Background(), dir)
	got := stderr.String()
	if !strings.Contains(got, "no baseline yet") {
		t.Fatalf("want a no-baseline warning, got %q", got)
	}
	if strings.Contains(got, "changed since the baseline") {
		t.Fatalf("no-baseline case must not claim a change: %q", got)
	}
}

// A failed Check surfaces as a warning, not silence.
func TestRoundStartRepoConfigWarnsOnCheckError(t *testing.T) {
	dir := t.TempDir() // not a git repository: Check fails
	var stderr bytes.Buffer
	c := &Ctx{Path: "rota round start", Deps: testDeps(), Stderr: &stderr, ctx: context.Background()}
	warnRepoConfigDrift(c, context.Background(), dir)
	if !strings.Contains(stderr.String(), "could not be checked") {
		t.Fatalf("want a warning for a failed check, got %q", stderr.String())
	}
}
