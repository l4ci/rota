package worker

import (
	"context"
	"github.com/l4ci/rota/internal/git"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Verification needs only a pinned plan and its process boundary. There is no
// worker registry, forge, config file, or earlier planning call in this test.
func TestTrainVerifySuppliedPlan(t *testing.T) {
	dir := t.TempDir()
	e := Env{Shell: func(_ context.Context, where, command string) (string, int) {
		if where != dir || command != "e2e" {
			t.Fatalf("unexpected verify %q in %q", command, where)
		}
		return "ok", 0
	}}.WithDefaults()
	tr := trainRun{e: e, ctx: bg, root: dir, cache: loadTrainCache(dir)}
	p := trainPlan{
		in:      gateInput{cfg: ParseGateConfig(decodeCfg(t, `{"test":{"full":["full"],"e2e":["e2e"]}}`))},
		res:     TrainResult{Members: []TrainMember{{Target: "kit", Branch: "feature"}}},
		baseSHA: "base", heads: []string{"head"}, scratch: dir,
		verify: func(where string) (VerifyResult, error) {
			if where != dir {
				t.Fatalf("full verify in %q", where)
			}
			return VerifyResult{Verified: []string{"full"}}, nil
		},
	}
	verified, result, done, err := tr.verifyStage(p)
	if err != nil || done || verified.passing != 1 || !slices.Equal(result.Verified, []string{"full"}) || !slices.Equal(result.E2EVerified, []string{"e2e"}) {
		t.Fatalf("verification of supplied plan: %+v prefix=%d done=%v err=%v", result, verified.passing, done, err)
	}
	// The handoff includes the plan that later checks and landing must consume.
	if verified.plan.baseSHA != "base" || verified.plan.heads[0] != "head" {
		t.Fatalf("lost pinned plan: %+v", verified.plan)
	}
}

// Planning owns a scratch tree before it can return its typed handoff. A panic
// there must release it just as cancellation and ordinary refusals do.
func TestTrainPlanPanicLeavesNoScratchTree(t *testing.T) {
	w := trainWorld(t, "true", "b1")
	e := w.env(false).WithDefaults()
	real := e.Git
	e.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
		if strings.Contains(dir, "rota-train-") && len(args) > 0 && args[0] == "merge" {
			panic("merge interrupted")
		}
		return real(ctx, dir, args...)
	}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = e.Train(bg, w.dir, TrainOpts{Base: "main", Targets: []string{"b1"}})
	}()
	extra := w.scratchTrees()
	// Keep a deliberately failing run from leaving its own test resource behind.
	for _, dir := range extra {
		e.cleanupGit(bg, w.dir, "worktree", "remove", "--force", dir)
		os.RemoveAll(filepath.Dir(dir))
	}
	if recovered != "merge interrupted" {
		t.Fatalf("expected the merge panic, got %v", recovered)
	}
	if len(extra) != 0 {
		t.Fatalf("scratch worktree left behind after planning panic: %v", extra)
	}
}
