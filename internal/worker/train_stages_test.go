package worker

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
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
		baseSHA: "base", heads: []string{"head"}, scratch: dir,
		verify: func(where string) (VerifyResult, error) {
			if where != dir {
				t.Fatalf("full verify in %q", where)
			}
			return VerifyResult{Verified: []string{"full"}}, nil
		},
	}
	verified, result, done, err := tr.verifyStage(p, TrainResult{Members: []TrainMember{{Target: "kit", Branch: "feature"}}})
	if err != nil || done || verified.passing != 1 || !slices.Equal(result.Verified, []string{"full"}) || !slices.Equal(result.E2EVerified, []string{"e2e"}) {
		t.Fatalf("verification of supplied plan: %+v prefix=%d done=%v err=%v", result, verified.passing, done, err)
	}
}

// A supplied prefix decides which members are checked. Refusing a later member
// must not change the caller's earlier culprit flags or notes.
func TestTrainVacuousSuppliedPrefix(t *testing.T) {
	dir := gittest.NewRepo(t, "main")
	gittest.Commit(t, dir, "base", "base.txt", "base")
	base := gittest.Run(t, dir, "rev-parse", "HEAD")
	gittest.Commit(t, dir, "first", "first.txt", "first")
	first := gittest.Run(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "work.test"), []byte("true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, dir, "add", "work.test")
	gittest.Commit(t, dir, "second", "second.txt", "second")
	second := gittest.Run(t, dir, "rev-parse", "HEAD")
	tr := trainRun{e: Env{Git: git.Exec}.WithDefaults(), ctx: bg, root: dir, o: TrainOpts{LandGreen: true}}
	p := trainPlan{tips: []string{base, first, second}, in: gateInput{cfg: ParseGateConfig(decodeCfg(t,
		`{"test":{"vacuousCheck":true,"filePattern":"\\.test$","vacuousCommand":"sh {files}"}}`))}}
	answer := TrainResult{Members: []TrainMember{{Target: "first", Culprit: true}, {Target: "second"}}}
	for _, c := range []struct {
		prefix  int
		verdict string
	}{{1, ""}, {2, GateVacuousTests}} {
		_, result, done, err := tr.vacuousStage(p, answer, trainVerified{passing: c.prefix})
		if err != nil || result.Verdict != c.verdict || done != (c.verdict != "") {
			t.Fatalf("prefix=%d result=%+v done=%v err=%v", c.prefix, result, done, err)
		}
		if done && (result.Culprit != "second" || result.Members[0].Culprit || !result.Members[1].Culprit) {
			t.Fatalf("wrong culprit: %+v", result)
		}
		if !answer.Members[0].Culprit || answer.Members[1].Culprit || len(answer.Notes) != 0 || answer.Verdict != "" {
			t.Fatalf("changed supplied answer: %+v", answer)
		}
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
