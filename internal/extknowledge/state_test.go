package extknowledge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/git"
)

func TestCloneState(t *testing.T) {
	fakeGit := func(inside bool, remotes string) git.Runner {
		return func(_ context.Context, _ string, args ...string) (git.Result, error) {
			switch args[0] {
			case "rev-parse":
				if inside {
					return git.Result{Stdout: "true\n"}, nil
				}
				return git.Result{ExitCode: 128, Stderr: "fatal: not a git repository"}, nil
			case "remote":
				return git.Result{Stdout: remotes}, nil
			}
			t.Errorf("unexpected git %v", args)
			return git.Result{}, nil
		}
	}
	dirWith := func(t *testing.T, policy bool) string {
		dir := t.TempDir()
		if policy {
			if err := os.WriteFile(filepath.Join(dir, PolicyFile), []byte(`{"write":"push"}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		dir  string
		git  git.Runner
		want State
	}{
		{"missing dir", filepath.Join(t.TempDir(), "gone"), fakeGit(true, "origin\n"), State{}},
		{"path is a file", file, fakeGit(true, "origin\n"), State{}},
		{"not git", dirWith(t, true), fakeGit(false, ""), State{Exists: true}},
		{"git without remote", dirWith(t, true), fakeGit(true, "\n"), State{Exists: true, IsGit: true, HasPolicy: true}},
		{"policy present", dirWith(t, true), fakeGit(true, "origin\n"), State{Exists: true, IsGit: true, HasRemote: true, HasPolicy: true}},
		{"policy absent", dirWith(t, false), fakeGit(true, "origin\n"), State{Exists: true, IsGit: true, HasRemote: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Clone{Dir: tc.dir, Env: Env{Git: tc.git}}
			if got := c.State(context.Background()); got != tc.want {
				t.Errorf("State = %+v, want %+v", got, tc.want)
			}
		})
	}
}
