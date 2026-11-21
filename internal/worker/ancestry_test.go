package worker

import (
	"context"
	"github.com/l4ci/rota/internal/landing"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/git"
)

// ancestryEnv answers every git call with exit 0 and stdout "sha", except the
// ancestry query, which exits with code.
func ancestryEnv(code int) Env {
	return Env{Config: diskConfig, Git: func(_ context.Context, _ string, args ...string) (git.Result, error) {
		if len(args) > 1 && args[0] == "merge-base" && args[1] == "--is-ancestor" {
			return git.Result{ExitCode: code, Stderr: "fatal: bad ref"}, nil
		}
		return git.Result{Stdout: "sha\n"}, nil
	}}.WithDefaults()
}

// An unreadable ref (exit 128) is an error at the reset, train and gate
// callers, never the "not an ancestor" answer of exit 1.
func TestAncestryErrorOutcomes(t *testing.T) {
	t.Run("reset refuses as unavailable", func(t *testing.T) {
		_, err := ancestryEnv(128).resetBase(t.TempDir(), "w1", "main")
		we, ok := err.(*exitcode.Error)
		if !ok || we.Exit != exitcode.ExitUnavailable {
			t.Fatalf("exit 128: %v", err)
		}
		if base, err := ancestryEnv(1).resetBase(t.TempDir(), "w1", "main"); err != nil || base != "main" {
			t.Fatalf("exit 1 keeps the local base: %q %v", base, err)
		}
	})
	t.Run("train refuses an unreadable integration tip", func(t *testing.T) {
		start := func(code int) error {
			run := &trainRun{e: ancestryEnv(code), ctx: bg, root: t.TempDir()}
			p := &trainPlanning{trainPlan: trainPlan{integration: &landing.Integration{Branch: "int", Assembled: "abc1234"}}}
			_, _, err := run.integrationStart(p)
			return err
		}
		we, ok := start(128).(*exitcode.Error)
		if !ok || we.Exit != exitcode.ExitUnavailable || !strings.Contains(we.Message, "could not compare") {
			t.Fatalf("exit 128: %v", we)
		}
		we, ok = start(1).(*exitcode.Error)
		if !ok || we.Exit != exitcode.ExitRefused || !strings.Contains(we.Message, "was rewritten") {
			t.Fatalf("exit 1 is the rewrite refusal: %v", we)
		}
	})
	t.Run("gate reports check-broke", func(t *testing.T) {
		verdict := func(code int) string {
			g := gateRun{e: ancestryEnv(code), ctx: bg, root: t.TempDir(), o: GateOpts{Slot: "w1", Base: "main"}}
			op := g.operation(landing.GateResult{Slot: "w1", Base: "main"}, GateTarget{}, gateForgeResult{})
			op.baseRef = "origin/main"
			op.confirmLanded("deadbeef")
			return string(op.res.Verdict)
		}
		if got := verdict(128); got != string(landing.GateCheckBroke) {
			t.Fatalf("exit 128: verdict %q", got)
		}
		if got := verdict(1); got != string(landing.GateNotOnBase) {
			t.Fatalf("exit 1: verdict %q", got)
		}
	})
}
