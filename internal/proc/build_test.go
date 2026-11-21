package proc

import (
	"context"
	"io"
	"os"
	"slices"
	"syscall"
	"testing"
)

func TestNewCmdEnvAndGroupRules(t *testing.T) {
	t.Setenv("PROC_BASE", "base")
	tests := []struct {
		name      string
		c         Cmd
		ctx       context.Context
		wantEnv   func(env []string) bool
		wantGroup bool
	}{
		{"neither inherits process env", Cmd{}, context.Background(),
			func(env []string) bool { return env == nil }, false},
		{"Env is added to os.Environ", Cmd{Env: []string{"PROC_X=1"}}, context.Background(),
			func(env []string) bool {
				return slices.Contains(env, "PROC_X=1") && slices.Contains(env, "PROC_BASE=base")
			}, false},
		{"Environ replaces os.Environ and Env", Cmd{Env: []string{"PROC_X=1"}, Environ: []string{"ONLY=1"}}, context.Background(),
			func(env []string) bool { return slices.Equal(env, []string{"ONLY=1"}) }, false},
		{"empty non-nil Environ is an empty environment", Cmd{Environ: []string{}}, context.Background(),
			func(env []string) bool { return env != nil && len(env) == 0 }, false},
		{"Group sets Setpgid", Cmd{Group: true}, context.Background(),
			func(env []string) bool { return env == nil }, true},
		{"nil ctx (detach) still honours Dir, env and Group", Cmd{Dir: os.TempDir(), Environ: []string{"A=1"}, Group: true}, nil,
			func(env []string) bool { return slices.Equal(env, []string{"A=1"}) }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.c.Name = "true"
			cmd := newCmd(tc.ctx, tc.c)
			if cmd.Dir != tc.c.Dir {
				t.Errorf("Dir = %q, want %q", cmd.Dir, tc.c.Dir)
			}
			if !tc.wantEnv(cmd.Env) {
				t.Errorf("Env = %v", cmd.Env)
			}
			if got := cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid; got != tc.wantGroup {
				t.Errorf("Setpgid = %v, want %v", got, tc.wantGroup)
			}
		})
	}
}

func TestDetachBuildsThroughNewCmd(t *testing.T) {
	dir := t.TempDir()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var pid, pgid int
	_, err = Run(context.Background(), Cmd{
		Name: "sh", Args: []string{"-c", "pwd; echo $PROC_D"},
		Dir: dir, Env: []string{"PROC_D=detached"}, Group: true, Detach: true,
		Stdio: &Stdio{Out: w},
		OnStart: func(p *os.Process) {
			pid = p.Pid
			pgid, _ = syscall.Getpgid(p.Pid)
		},
	})
	w.Close()
	if err != nil {
		t.Fatal(err)
	}
	if pgid != pid {
		t.Errorf("detached Group command: pgid = %d, want its own group %d", pgid, pid)
	}
	out, _ := io.ReadAll(r)
	if want := dir + "\ndetached\n"; string(out) != want {
		t.Errorf("Dir/Env not applied: output %q, want %q", out, want)
	}
}
