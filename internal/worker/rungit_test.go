package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/git"
)

// A git that cannot run (missing binary, cancelled context) keeps its typed
// error through runGitErr; runGit alone folds it into exit 127.
func TestRunGitErrKeepsTypedError(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, want := range map[string]error{
		"no git":    git.ErrNoGit,
		"cancelled": context.Canceled,
	} {
		t.Run(name, func(t *testing.T) {
			e := Env{Git: func(context.Context, string, ...string) (git.Result, error) {
				return git.Result{}, want
			}}
			out, code, err := e.runGitErr(cancelled, "", "status")
			if !errors.Is(err, want) || out != "" || code != 127 {
				t.Fatalf("runGitErr = %q, %d, %v; want typed %v", out, code, err, want)
			}
			if _, code := e.runGit(cancelled, "", "status"); code != 127 {
				t.Fatalf("runGit code = %d, want 127", code)
			}
		})
	}
	e := Env{Git: func(context.Context, string, ...string) (git.Result, error) {
		return git.Result{Stdout: "x\n", ExitCode: 128}, nil
	}}
	if out, code, err := e.runGitErr(context.Background(), "", "status"); err != nil || out != "x" || code != 128 {
		t.Fatalf("exit status = %q, %d, %v; want x, 128, nil", out, code, err)
	}
}
