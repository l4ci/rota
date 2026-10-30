package host

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/testiso"
)

// TestMain puts tripwire herdr, tmux, ps, gh and glab first on PATH and
// (the host env is scrubbed by testiso), so a test that forgets to inject its Runner fails
// instead of reaching a live herdr server or tmux session. This round runs
// inside both: a stray `herdr tab close` kills live agents.
func TestMain(m *testing.M) {
	tw, err := testiso.NewTripwire("host-tripwire", "herdr", "tmux", "gh", "glab")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	if hit := tw.Check(); hit != "" {
		fmt.Fprintf(os.Stderr, "FAIL: tests exec'd a real host binary from PATH:\n%s", hit)
		code = 1
	}
	tw.Close()
	os.Exit(code)
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
	if name != "herdr" && name != "tmux" {
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
