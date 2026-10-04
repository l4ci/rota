package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var tripwireHit string

// TestMain puts tripwire herdr, tmux, ps, gh and glab first on PATH and
// clears the host env, so a test that forgets to inject its Runner fails
// instead of reaching a live herdr server or tmux session. This round runs
// inside both: a stray `herdr tab close` kills live agents.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "host-tripwire")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tripwireHit = filepath.Join(dir, "hit")
	for _, bin := range []string{"herdr", "tmux", "gh", "glab"} {
		script := fmt.Sprintf("#!/bin/sh\necho \"$0 $*\" >> '%s'\necho 'tripwire: real %s reached from a test' >&2\nexit 99\n", tripwireHit, bin)
		if err := os.WriteFile(filepath.Join(dir, bin), []byte(script), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Root the temp dirs of child processes (the old bin/ helpers mktemp) under
	// the tripwire dir, which is removed below (#110).
	if tmp := filepath.Join(dir, "tmp"); os.Mkdir(tmp, 0o755) == nil {
		os.Setenv("TMPDIR", tmp)
	}
	for _, k := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_WORKSPACE_ID", "HERDR_PANE_ID", "HERDR_SOCKET_PATH"} {
		os.Unsetenv(k)
	}
	code := m.Run()
	if b, err := os.ReadFile(tripwireHit); err == nil {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd a real host binary from PATH:\n%s", b)
		code = 1
	}
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestTripwireCatchesRealBinary(t *testing.T) {
	r, err := ExecRunner(context.Background(), "herdr", []string{"tab", "close", "x"})
	if err != nil || r.ExitCode != 99 {
		t.Fatalf("ExecRunner did not hit the tripwire: %+v, %v", r, err)
	}
	os.Remove(tripwireHit) // hit on purpose
}

// fake is a scripted Runner: handler answers each call; every call is logged.
type fake struct {
	mu      sync.Mutex
	calls   []string
	handler func(name string, args []string) Result
}

func (f *fake) run(_ context.Context, name string, args []string) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name != "herdr" && name != "tmux" && name != "ps" {
		panic("unexpected command " + name)
	}
	return f.handler(name, args), nil
}

func (f *fake) log() string { f.mu.Lock(); defer f.mu.Unlock(); return strings.Join(f.calls, "\n") }

func (f *fake) count(prefix string) int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

type clock struct{ slept time.Duration }

func (c *clock) sleep(d time.Duration) { c.slept += d }

func deps(f *fake, env map[string]string, c *clock) Deps {
	return Deps{
		Run:      f.run,
		Getenv:   func(k string) string { return env[k] },
		Sleep:    c.sleep,
		Alive:    func(int) bool { return false },
		Tree:     func(p int) []int { return []int{p} },
		LookPath: func(n string) (string, error) { return "/fake/" + n, nil },
		KillWait: 3,
	}
}
