package git

import (
	"context"
	"testing"
)

func TestRepoRunnerReceivesDirAndArgs(t *testing.T) {
	var gotDir string
	var gotArgs []string
	r := Repo{Dir: "/x", Runner: func(_ context.Context, dir string, args ...string) (Result, error) {
		gotDir, gotArgs = dir, args
		return Result{Stdout: "feat\n"}, nil
	}}
	b, err := r.CurrentBranch(context.Background())
	if err != nil || b != "feat" {
		t.Fatalf("CurrentBranch = %q, %v", b, err)
	}
	if gotDir != "/x" || len(gotArgs) == 0 || gotArgs[0] != "rev-parse" {
		t.Fatalf("runner saw dir=%q args=%v", gotDir, gotArgs)
	}
}

func TestRepoRunnerReachesEveryMethod(t *testing.T) {
	var calls int
	r := Repo{Dir: "/x", Runner: func(context.Context, string, ...string) (Result, error) {
		calls++
		return Result{ExitCode: 1}, nil
	}}
	ctx := context.Background()
	r.ChangedLines(ctx, "a...b")
	if calls < 1 {
		t.Fatalf("runner saw %d calls, want one per method at least", calls)
	}
}
