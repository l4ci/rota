package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tui"
	"github.com/l4ci/rota/internal/worker"
)

// perfFixture is #1841's representative round: four local slots (working,
// awaiting review, parked, unavailable), twelve open issues, two support items
// waiting and one claimed, and two retained review PRs. Every forge call takes
// perfLatency, so the numbers compare a slow tracker, not this machine.
const perfLatency = 40 * time.Millisecond

type perfRig struct {
	root  string
	deps  *Deps
	mu    sync.Mutex
	calls []string
	// failIf makes a matching call exit 1 (nil: none fail).
	failIf func(joined string) bool
}

func newPerfRig(t *testing.T) *perfRig {
	t.Helper()
	root := gitRepo(t)
	r := &perfRig{root: root}
	for path, body := range map[string]string{
		filepath.Join(root, ".rota", "config.json"): `{"forge":{"provider":"github"},"issues":{"source":"github"},"round":{"support":"mac"}}`,
		worker.RegistryPath(root):                   `{"host":"herdr","slots":[{"name":"ben","issue":"1","branch":"ben/1-work","state":"working"},{"name":"dana","issue":"2","branch":"dana/2-review","state":"done","pr":"https://github.com/acme/project/pull/202"},{"name":"kit","branch":"park/kit","state":"idle"},{"name":"nia","issue":"3","branch":"nia/3-work","state":"dead"}],"prs":[{"issue":"4","pr":"https://github.com/acme/project/pull/204","from":"ben"},{"issue":"5","pr":"https://github.com/acme/project/pull/205","from":"dana"}]}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var issues []string
	for n := 1; n <= 12; n++ {
		labels := "[]"
		if n >= 10 { // 10, 11 wait; 12 is claimed by the support machine
			labels = `[{"name":"site:support"}]`
		}
		issues = append(issues, fmt.Sprintf(`{"number":%d,"title":"item %d","body":"## Acceptance\n- [ ] x","labels":%s,"state":"OPEN","url":"https://github.com/acme/project/issues/%d"}`, n, n, labels, n))
	}
	list := "[" + strings.Join(issues, ",") + "]"
	d := testDeps()
	d.IsTerminal = func(any) bool { return true }
	d.TrackerOptions = []tracker.Option{tracker.WithExec(func(ctx context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		joined := strings.Join(args, " ")
		r.mu.Lock()
		r.calls = append(r.calls, name+" "+joined)
		r.mu.Unlock()
		if r.failIf != nil && r.failIf(joined) {
			return nil, []byte("boom"), 1, nil
		}
		select {
		case <-time.After(perfLatency):
		case <-ctx.Done():
			return nil, nil, 1, ctx.Err()
		}
		switch {
		case len(args) > 1 && args[0] == "issue" && args[1] == "list" && !strings.Contains(joined, "--label"):
			return []byte(list), nil, 0, nil
		case len(args) > 1 && args[0] == "issue" && args[1] == "view":
			return []byte(issues[0]), nil, 0, nil
		case strings.Contains(joined, "issues/12/comments"):
			return []byte(`[{"id":1,"body":"<!-- rota:claim mac-ben -->\nClaimed by mac-ben","user":{"login":"x"},"created_at":"2026-01-01T00:00:00Z"}]`), nil, 0, nil
		}
		return []byte("[]"), nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}
	r.deps = d
	return r
}

func (r *perfRig) callCount() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.calls) }

func (r *perfRig) callList() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// perfRuns is how many times the fixture is measured. The targets bind the
// median, so one slow run on a loaded gate machine does not fail the test;
// the ceilings bind the slowest run, so a real regression still does.
const perfRuns = 5

func median(d []time.Duration) time.Duration {
	s := append([]time.Duration(nil), d...)
	slices.Sort(s)
	return s[len(s)/2]
}

// TestRoundOverviewPerformanceFixture records first-frame time, key latency
// while a pass is in flight, one pass's duration and its tracker calls. The
// targets are #1841 AC-1's: 0.5 s and 0.1 s, held by the median of perfRuns
// runs; the ceilings (2 s, 0.5 s) hold for every run. A single loose bound let
// a 4x regression pass (#1873 AC-9), and no gate flake at the targets is on
// record, so the targets are back.
func TestRoundOverviewPerformanceFixture(t *testing.T) {
	r := newPerfRig(t)
	t.Setenv("TERM", "xterm")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(r.root)

	var firsts, keys, passes []time.Duration
	var callsAtFirstFrame, passCalls int
	var frame string
	r.deps.RunView = func(_ *Ctx, model tui.Model) error {
		// The command's context lives while its screen runs, as in the real
		// runView: the pass reads the tracker under it.
		callsAtFirstFrame = r.callCount()
		cmd := model.(*roundScreen).Init()
		passDone := make(chan time.Duration, 1)
		passStart := time.Now()
		go func() { cmd.Async(context.Background()); passDone <- time.Since(passStart) }()
		// Keys are handled by Update at once while the pass waits on the tracker.
		for _, k := range []string{"j", "j", "k", "i", "p"} {
			t0 := time.Now()
			model, _ = press(model, k)
			model.Render(120, 40, tui.Style{})
			keys = append(keys, time.Since(t0))
		}
		passes = append(passes, <-passDone)
		passCalls = r.callCount() - callsAtFirstFrame
		model, _ = model.Update(tui.Done{Tag: cmd.Tag})
		frame = tui.Strip(model.Render(120, 40, tui.Style{}))
		return nil
	}
	for i := 0; i < perfRuns; i++ {
		r.mu.Lock()
		r.calls = nil
		r.mu.Unlock()
		started := time.Now()
		var out, errs bytes.Buffer
		first := time.Duration(0)
		inner := r.deps.RunView
		r.deps.RunView = func(c *Ctx, m tui.Model) error {
			first = time.Since(started)
			return inner(c, m)
		}
		code := mainWith(r.deps, []string{"round", "status", "--ui"}, strings.NewReader(""), &out, &errs)
		r.deps.RunView = inner
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errs.String())
		}
		firsts = append(firsts, first)
		if callsAtFirstFrame != 0 {
			t.Errorf("%d tracker calls before the first frame", callsAtFirstFrame)
		}
		for _, want := range []string{"Support queue (3)", "waiting", "active", "mac-ben", "PR review (", "Active ("} {
			if !strings.Contains(frame, want) {
				t.Errorf("loaded frame lacks %q:\n%s", want, frame)
			}
		}
	}

	t.Logf("first frame median %s (max %s) with 0 tracker calls before it; pass median %s with %d calls; key median %s (max %s)",
		median(firsts), slices.Max(firsts), median(passes), passCalls, median(keys), slices.Max(keys))
	if m := median(firsts); m > 500*time.Millisecond {
		t.Errorf("median first frame %s, target 0.5 s", m)
	}
	if m := slices.Max(firsts); m > 2*time.Second {
		t.Errorf("slowest first frame %s, ceiling 2 s", m)
	}
	if m := median(keys); m > 100*time.Millisecond {
		t.Errorf("median key took %s while loading, target 0.1 s", m)
	}
	if m := slices.Max(keys); m > 500*time.Millisecond {
		t.Errorf("slowest key took %s while loading, ceiling 0.5 s", m)
	}
}
