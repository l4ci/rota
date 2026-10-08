package cli

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// forgeBuilds counts the forge adapters the Deps construct: every adapter
// applies its options once, so a counting option sees each build.
func forgeBuilds(d *Deps) *int {
	n := new(int)
	d.TrackerOptions = append(d.TrackerOptions, func(*tracker.CLI) { *n++ })
	return n
}

// withForge builds a forge, withForge=false none: the probe sees the build
// itself, not whether a caller's fake was reached.
func TestRoundEnvFactoriesBuildAForgeOnlyWhenAsked(t *testing.T) {
	root := trackerProject(t, "")
	gitIn(t, root, "init", "-q")
	d := testDeps()
	builds := forgeBuilds(d)

	e := d.RoundEnvLocal(context.Background(), root)
	if *builds != 0 || e.Forge != nil || e.ForgeErr != "" {
		t.Errorf("RoundEnvLocal: %d forge build(s), Forge %v, ForgeErr %q", *builds, e.Forge, e.ForgeErr)
	}

	e = d.RoundEnv(context.Background(), root)
	if *builds != 1 {
		t.Fatalf("RoundEnv built %d forge(s), want 1 (the probe is blind otherwise)", *builds)
	}
	if e.Forge == nil && e.ForgeErr == "" {
		t.Errorf("RoundEnv left Forge and ForgeErr both empty")
	}
}
