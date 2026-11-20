package land

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/git"
)

func TestGitOut(t *testing.T) {
	boom := errors.New("cannot run git")
	cases := []struct {
		name    string
		res     git.Result
		runErr  error
		want    string
		wantErr string
		isGit   bool
	}{
		{name: "exit 0 trims trailing newlines", res: git.Result{Stdout: "abc123\n\n"}, want: "abc123"},
		{name: "non-zero names label and first stderr line", res: git.Result{ExitCode: 128, Stdout: "partial\n", Stderr: "fatal: bad ref\nhint: more\n"}, want: "partial", wantErr: "git rev-parse x: fatal: bad ref", isGit: true},
		{name: "runner error passes through", runErr: boom, wantErr: "cannot run git"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			run := func(args ...string) (git.Result, error) {
				got = args
				return c.res, c.runErr
			}
			out, err := GitOut(run, "rev-parse x", "rev-parse", "x")
			if len(got) != 2 || got[0] != "rev-parse" || got[1] != "x" {
				t.Fatalf("args = %q", got)
			}
			if out != c.want {
				t.Errorf("out = %q, want %q", out, c.want)
			}
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err == nil || err.Error() != c.wantErr {
				t.Fatalf("err = %v, want %q", err, c.wantErr)
			}
			var ge *GitError
			if errors.As(err, &ge) != c.isGit {
				t.Errorf("errors.As GitError = %v, want %v", !c.isGit, c.isGit)
			}
		})
	}
}
