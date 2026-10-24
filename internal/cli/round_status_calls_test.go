package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/palette"
	"github.com/l4ci/rota/internal/round"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/tui"
	"github.com/l4ci/rota/internal/worker"
)

// The command must hand a useful registry frame to the terminal before waiting
// for any tracker process. Blocking at the real process boundary also catches
// eager reads in dispatch, environment construction and view initialization.
func TestRoundOverviewResponsive(t *testing.T) {
	root := gitRepo(t)
	t.Setenv("TERM", "xterm")
	for path, body := range map[string]string{
		filepath.Join(root, ".rota", "config.json"): `{"forge":{"provider":"github"},"issues":{"source":"github"}}`,
		worker.RegistryPath(root):                   `{"host":"herdr","slots":[{"name":"ben","issue":"101","branch":"ben/101-work","state":"working"},{"name":"dana","issue":"102","branch":"dana/102-review","state":"done","pr":"https://github.com/acme/project/pull/202"},{"name":"kit","branch":"park/kit","state":"idle"},{"name":"nia","issue":"103","branch":"nia/103-work","state":"dead"}],"prs":[{"issue":"104","pr":"https://github.com/acme/project/pull/204","from":"ben"},{"issue":"105","pr":"https://github.com/acme/project/pull/205","from":"dana"}]}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	blocked, release := make(chan struct{}), make(chan struct{})
	frame := make(chan string, 1)
	done := make(chan int, 1)
	var once sync.Once
	d := testDeps()
	d.IsTerminal = func(any) bool { return true }
	d.TrackerOptions = []tracker.Option{tracker.WithExec(func(ctx context.Context, _, _ string, _ []string, _ []byte) ([]byte, []byte, int, error) {
		once.Do(func() { close(blocked) })
		select {
		case <-release:
			return []byte("[]"), nil, 0, nil
		case <-ctx.Done():
			return nil, nil, 1, ctx.Err()
		}
	}, func(name string) (string, error) { return "/fake/" + name, nil })}
	d.RunView = func(_ *Ctx, m tui.Model) error {
		frame <- tui.Strip(m.Render(120, 40, tui.Style{}))
		return nil
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	started := time.Now()
	go func() { done <- mainWith(d, []string{"round", "status", "--ui"}, strings.NewReader(""), &out, &errs) }()
	defer func() {
		close(release)
		code := <-done
		os.Chdir(old)
		t.Logf("command completed after %s (exit %d)", time.Since(started), code)
	}()
	select {
	case rendered := <-frame:
		if !strings.Contains(rendered, "ben") || !strings.Contains(rendered, "101") || !strings.Contains(rendered, "kit") {
			t.Fatalf("first frame lost local work or parked slots:\n%s", rendered)
		}
		t.Logf("first local frame: %s", time.Since(started))
	case <-blocked:
		// Channel ordering, rather than a latency threshold, proves startup
		// attempted an external read before handing its frame to RunView.
		select {
		case <-frame:
		default:
			t.Fatal("first local frame is blocked behind a tracker read")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("command produced neither a local frame nor a tracker read")
	}
}

// TestRoundStatusReadsTheForgeOnce pins the gh calls of one `round status` on
// a repo with seven open issues: no identical call is made twice, an issue the
// list returned is not viewed again, a label-filtered open list is answered
// from the open list, and the total stays small. The same cache serves every
// verb, so candidates and start get the same saving.
func TestRoundStatusReadsTheForgeOnce(t *testing.T) {
	root := gitRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"),
		[]byte(`{"forge": {"provider": "github"}, "issues": {"source": "github"}}`), 0o666); err != nil {
		t.Fatal(err)
	}
	var issues []string
	for n := 1; n <= 7; n++ {
		issues = append(issues, fmt.Sprintf(`{"number":%d,"title":"item %d","body":"body","labels":[],"state":"OPEN"}`, n, n))
	}
	list := "[" + strings.Join(issues, ",") + "]"

	var mu sync.Mutex
	var calls []string
	deps := testDeps()
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
		mu.Lock()
		calls = append(calls, name+" "+strings.Join(args, " "))
		mu.Unlock()
		switch {
		case len(args) > 1 && args[0] == "issue" && args[1] == "list" && !strings.Contains(strings.Join(args, " "), "--label"):
			return []byte(list), nil, 0, nil
		case len(args) > 1 && args[0] == "issue" && args[1] == "view":
			return []byte(issues[0]), nil, 0, nil
		}
		return []byte("[]"), nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}

	if code, out, errOut := rotaInWith(t, deps, root, "--json", "round", "status"); code != 0 {
		t.Fatalf("status exit %d: %s %s", code, out, errOut)
	}
	seen := map[string]bool{}
	for _, c := range calls {
		if seen[c] {
			t.Errorf("call made twice: %s", c)
		}
		seen[c] = true
		if strings.Contains(c, "issue list --state open") && strings.Contains(c, "--label") {
			t.Errorf("a label-filtered open list went to the forge: %s", c)
		}
		if strings.Contains(c, " issue view ") {
			t.Errorf("a listed issue was viewed again: %s", c)
		}
	}
	if len(calls) > 10 {
		t.Errorf("%d forge calls, want at most 10:\n%s", len(calls), strings.Join(calls, "\n"))
	}
}

// The screen opens on registry rows, starts its first pass from Init, and keeps
// that data (stamped honestly) when the pass fails; a second request during a
// pass is dropped.
func TestRoundOverviewRefreshLifecycle(t *testing.T) {
	clk := newClock()
	local := roundSnap{Slots: []round.Row{{Name: "ben", Issue: "101", Branch: "ben/101-work"}}}
	gate, fail := make(chan struct{}), errors.New("forge: down")
	passes := 0
	load := func(ctx context.Context) (roundSnap, error) {
		passes++
		select {
		case <-gate:
		case <-ctx.Done():
			return roundSnap{}, ctx.Err()
		}
		return roundSnap{}, fail
	}
	m := newRoundScreen(local, load, clk.now, roundRefresh)
	m.loading = true
	cmd := m.Init()
	if cmd.Async == nil {
		t.Fatal("Init did not start the first pass")
	}
	if out := tui.Strip(m.Render(100, 20, tui.Style{})); !strings.Contains(out, "loading…") || !strings.Contains(out, "ben") {
		t.Errorf("first frame:\n%s", out)
	}
	if _, again := press(m, "r"); again.Async != nil {
		t.Error("r during the first pass started a second")
	}
	close(gate)
	done := runCmd(m, cmd)
	out := tui.Strip(done.Render(100, 20, tui.Style{}))
	if !strings.Contains(out, "refresh failed: forge: down") || !strings.Contains(out, "ben") || !strings.Contains(out, "not loaded") {
		t.Errorf("failed first pass must keep the local rows and say the data is not loaded:\n%s", out)
	}
	if passes != 1 {
		t.Errorf("passes = %d", passes)
	}
}

// The palette installs a screen without being its root model: it must still
// start the screen's first pass, the pass must read the tracker under a context
// that outlives the palette's own run() (paletteView returns before the screen
// is shown), and the result must reach the screen with no further key. This
// goes through the real entry: paletteVerb -> paletteView -> roundStatusView.
func TestRoundOverviewFromPalette(t *testing.T) {
	r := newPerfRig(t)
	t.Setenv("TERM", "xterm")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(r.root)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	c := &Ctx{Deps: r.deps, Stdin: pr, Stdout: io.Discard, Stderr: io.Discard}
	entry := paletteVerb(c, Tree(), "Round status", "", palette.Always, "round", "status")
	if entry.View == nil {
		t.Fatal("round status has no palette view")
	}
	out := &syncBuf{}
	done := make(chan error, 1)
	go func() {
		done <- palette.Run(palette.Config{
			Entries: []palette.Entry{entry}, InProject: true, In: pr, Out: out, Color: true,
			MakeRaw: func() (func(), error) { return func() {}, nil },
			Signals: make(chan os.Signal), Exit: func(int) {},
		})
	}()
	pw.WriteString("1") // open the entry; no other key follows
	deadline := time.After(10 * time.Second)
	for !strings.Contains(tui.Strip(out.String()), "Support queue (3)") {
		select {
		case <-deadline:
			t.Fatalf("the palette-hosted screen never loaded:\n%s", tui.Strip(out.String()))
		case <-time.After(10 * time.Millisecond):
		}
	}
	frame := out.String()
	if strings.Contains(tui.Strip(frame[strings.LastIndex(frame, tui.ClearScreen):]), "context canceled") {
		t.Errorf("a read ran under a cancelled context:\n%s", tui.Strip(frame))
	}
	if !strings.Contains(frame, "\x1b]8;;https://github.com/acme/project/issues/") {
		t.Errorf("palette frames lack the issue hyperlinks: %q", frame)
	}
	pw.WriteString("qq")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// A Done from a screen that was closed must not reach the one opened after it,
// and closing a screen cancels its pass.
func TestRoundOverviewReopenDropsOldPass(t *testing.T) {
	local := roundSnap{Slots: []round.Row{{Name: "ben", Issue: "101", Branch: "ben/101-work"}}}
	cancelled := make(chan struct{})
	load := func(ctx context.Context) (roundSnap, error) {
		<-ctx.Done()
		close(cancelled)
		return roundSnap{}, ctx.Err()
	}
	first := openingScreen(newRoundScreen(local, load, time.Now, roundRefresh))
	cmd := first.Init()
	finished := make(chan error, 1)
	go func() { finished <- cmd.Async(context.Background()) }()
	for first.box.cancelSet() == false {
		time.Sleep(time.Millisecond)
	}
	if _, q := press(first, "q"); !q.Quit {
		t.Fatal("q did not leave")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("leaving did not cancel the pass")
	}
	<-finished
	second := openingScreen(newRoundScreen(local, load, time.Now, roundRefresh))
	second2, _ := second.Update(tui.Done{Tag: cmd.Tag}) // the old pass reports in
	out := tui.Strip(second2.Render(100, 20, tui.Style{}))
	if !strings.Contains(out, "loading…") || strings.Contains(out, "updated ") {
		t.Errorf("an old pass's Done reached the new screen:\n%s", out)
	}
	if _, again := press(second2, "r"); again.Async != nil {
		t.Error("r on a screen whose first pass is running started a second")
	}
}

// openingScreen is a screen opened from a palette: loading until Init runs.
func openingScreen(m *roundScreen) *roundScreen { m.loading = true; return m }

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// The status screen reads Deps.Now, so a pinned clock reaches its age labels.
func TestRoundStatusViewUsesDepsClock(t *testing.T) {
	r := newPerfRig(t)
	pinNow(r.deps, "2031-05-06T07:08:09Z")
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(r.root)
	m, err := roundStatusView(&Ctx{Deps: r.deps}, Result{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m.(*roundScreen).now(), r.deps.Now(); !got.Equal(want) {
		t.Errorf("screen clock = %v, want Deps.Now %v", got, want)
	}
}
