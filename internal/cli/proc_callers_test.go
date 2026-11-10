package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/l4ci/rota/internal/proc"
)

// recordProc is a proc.Runner fake: it keeps every Cmd and answers with res.
func recordProc(res proc.Result, err error) (*[]proc.Cmd, proc.Runner) {
	var got []proc.Cmd
	return &got, func(_ context.Context, c proc.Cmd) (proc.Result, error) {
		got = append(got, c)
		if c.OnStart != nil {
			c.OnStart(&os.Process{Pid: 4242})
		}
		return res, err
	}
}

func TestStatuslineThenRunsThroughProcOnTheCallersStreams(t *testing.T) {
	dir := gitRepo(t)
	got, run := recordProc(proc.Result{ExitCode: 7}, nil)
	d := depsIn(dir)
	d.Proc = run
	var so, se bytes.Buffer
	code := mainWith(d, []string{"statusline", "dump", "--then", "cat"}, strings.NewReader("{}"), &so, &se)
	if code != 0 || len(*got) != 1 {
		t.Fatalf("exit %d, %d proc calls\n%s", code, len(*got), se.String())
	}
	c := (*got)[0]
	in, _ := io.ReadAll(c.Stdio.In)
	if c.Name != "sh" || !slices.Equal(c.Args, []string{"-c", "cat"}) || c.Timeout != proc.NoTimeout || string(in) != "{}" || c.Stdio.Out == nil || c.Stdio.Err == nil {
		t.Fatalf("cmd %+v input %q", c, in)
	}
}

func TestTestLockMapsProcResultToTheVerbExit(t *testing.T) {
	root := tierProject(t, `{}`)
	for _, tc := range []struct {
		name string
		res  proc.Result
		want int
	}{
		{"exit code", proc.Result{ExitCode: 5}, 5},
		{"signalled", proc.Result{ExitCode: -1, Signal: int(syscall.SIGTERM)}, 143},
		{"clean", proc.Result{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, run := recordProc(tc.res, nil)
			d := testDeps()
			d.Proc = run
			o := trRunWith(t, d, root, "", "test", "lock", "--", "mycmd", "a")
			if o.code != tc.want || len(*got) != 1 {
				t.Fatalf("exit %d want %d, %d calls\n%s", o.code, tc.want, len(*got), o.stderr)
			}
			c := (*got)[0]
			if c.Name != "mycmd" || !slices.Equal(c.Args, []string{"a"}) || !slices.Contains(c.Env, testLockHeldEnv+"=1") || c.Stdio == nil || c.Grace == 0 {
				t.Fatalf("cmd %+v", c)
			}
		})
	}
}

func TestOpenBrowserDetachesThroughProc(t *testing.T) {
	for goos, name := range map[string]string{"linux": "xdg-open", "darwin": "open", "windows": "rundll32"} {
		got, run := recordProc(proc.Result{}, nil)
		if err := openBrowserWith(run, goos, "https://example.com/x"); err != nil {
			t.Fatal(err)
		}
		if len(*got) != 1 || (*got)[0].Name != name || !(*got)[0].Detach || !strings.HasSuffix((*got)[0].Args[len((*got)[0].Args)-1], "https://example.com/x") {
			t.Errorf("%s: %+v", goos, *got)
		}
	}
	got, run := recordProc(proc.Result{}, nil)
	if err := openBrowserWith(run, "linux", "file:///etc/passwd"); err == nil || len(*got) != 0 {
		t.Errorf("unsafe URL: err %v, calls %d", err, len(*got))
	}
}

func TestKeepaliveSpawnRunsThroughProc(t *testing.T) {
	got, run := recordProc(proc.Result{ExitCode: -1, Signal: int(syscall.SIGKILL)}, nil)
	d := testDeps()
	d.Proc = run
	c := &Ctx{Deps: d, Stdout: io.Discard, Stderr: io.Discard}
	child, err := keepaliveSpawn(c, "/cfg")(([]string{"agentbin", "--x"}), []string{"K=V"})
	if err != nil {
		t.Fatal(err)
	}
	exit, err := child.Wait()
	if err != nil || exit.Code != 137 || exit.Signal != "SIGKILL" {
		t.Fatalf("exit %+v err %v", exit, err)
	}
	pc := (*got)[0]
	if pc.Name != "agentbin" || pc.Timeout != proc.NoTimeout || pc.Group || pc.Stdio == nil ||
		!slices.Contains(pc.Environ, "CLAUDE_CONFIG_DIR=/cfg") || !slices.Contains(pc.Environ, "K=V") {
		t.Fatalf("cmd %+v", pc)
	}
}
