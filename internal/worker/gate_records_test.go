package worker

import (
	"context"
	"github.com/l4ci/rota/internal/landing"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
)

func TestGateVerdictRequiresResolvedHead(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		code int
	}{
		{name: "failed lookup", code: 128},
		{name: "empty success"},
		{name: "whitespace success", out: " \n"},
		{name: "failed lookup with output", out: "abcdef0", code: 128},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Env{Config: diskConfig, Git: func(_ context.Context, _ string, args ...string) (git.Result, error) {
				if strings.Join(args, " ") != "rev-parse --short feature" {
					t.Fatalf("unexpected git call: %v", args)
				}
				return git.Result{Stdout: tc.out, ExitCode: tc.code}, nil
			}}.WithDefaults()
			g := gateRun{e: e, ctx: bg, root: t.TempDir(), o: GateOpts{Slot: "nia"}}
			answer := landing.GateResult{Branch: "feature"}
			_, result, done, err := g.stepVerdict(answer, GateTarget{Branch: "feature"}, gateForgeResult{}, gateRefsResult{head: "feature"})
			if err != nil || !done || result.Verdict != landing.GateCheckBroke || !strings.Contains(result.Err, "feature") {
				t.Fatalf("unresolved head must refuse: result=%+v done=%v err=%v", result, done, err)
			}
			if answer.Verdict != "" || answer.Err != "" {
				t.Fatalf("changed supplied answer: %+v", answer)
			}
		})
	}
}
