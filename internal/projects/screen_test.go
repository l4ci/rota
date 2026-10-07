package projects

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/golden"
	"github.com/l4ci/rota/internal/tui"
)

const gw, gh = 100, 30

var st = tui.Style{Color: true}

func sampleRows() []Row {
	return []Row{
		{Name: "alpha", Path: "/work/alpha", LastSeen: "2026-10-01T10:00:00Z", Status: "ok", Round: "7", Lease: "held by pid 4242"},
		{Name: "beta", Path: "/work/beta", LastSeen: "2026-09-12T08:30:00Z", Status: "no .rota/", Round: "-", Lease: "-"},
		{Name: "gamma", Path: "/mnt/old/gamma", LastSeen: "2026-08-03T16:45:00Z", Status: "missing directory", Round: "-", Lease: "-"},
		{Name: "delta", Path: "/work/delta", LastSeen: "2026-10-05T12:00:00Z", Status: "ok", Round: "none", Lease: "stale (holder gone)"},
	}
}

// rig is a screen with a fake verb runner: it records each call instead of
// running it, and Exec'd Cmds are run by apply the way the driver would.
type rig struct {
	t     *testing.T
	s     tui.Model
	calls [][]string
	rows  []Row
	err   error
}

func newRig(t *testing.T) *rig {
	r := &rig{t: t, rows: sampleRows()}
	r.s = NewScreen(r.rows, func() ([]Row, error) { return r.rows, nil }, func(args ...string) error {
		r.calls = append(r.calls, args)
		return r.err
	})
	return r
}

func k(name string) tui.Key {
	switch name {
	case "up":
		return tui.Key{Kind: tui.KeyUp}
	case "down":
		return tui.Key{Kind: tui.KeyDown}
	case "enter":
		return tui.Key{Kind: tui.KeyEnter}
	case "esc":
		return tui.Key{Kind: tui.KeyEsc}
	case "backspace":
		return tui.Key{Kind: tui.KeyBackspace}
	}
	return tui.Rune([]rune(name)[0])
}

// press sends keys; a Cmd with an Exec runs and its Done goes back in. It
// returns the last Cmd.
func (r *rig) press(names ...string) (last tui.Cmd) {
	for _, n := range names {
		var cmd tui.Cmd
		r.s, cmd = r.s.Update(k(n))
		last = cmd
		if cmd.Exec != nil {
			r.s, _ = r.s.Update(tui.Done{Err: cmd.Exec()})
		}
	}
	return last
}

func (r *rig) frame() []string {
	r.t.Helper()
	lines := strings.Split(tui.Strip(r.s.Render(gw, gh, st)), "\n")
	if len(lines) != gh {
		r.t.Errorf("frame has %d lines, want %d", len(lines), gh)
	}
	for i, l := range lines {
		if n := utf8.RuneCountInString(l); n > gw {
			r.t.Errorf("line %d is %d runes: %q", i, n, l)
		}
	}
	return lines
}

type in struct {
	Keys []string `json:"keys"`
	W    int      `json:"w"`
	H    int      `json:"h"`
}

func typed(s string) []string {
	var out []string
	for _, c := range s {
		out = append(out, string(c))
	}
	return out
}

func TestScreenGolden(t *testing.T) {
	r := newRig(t)
	golden.Check(t, in{nil, gw, gh}, r.frame())

	r.press("down", "down")
	golden.Check(t, in{[]string{"down", "down"}, gw, gh}, r.frame())
}

func TestScreenFilterGolden(t *testing.T) {
	r := newRig(t)
	keys := append([]string{"/"}, typed("ta")...)
	r.press(keys...)
	golden.Check(t, in{keys, gw, gh}, r.frame())
}

func TestScreenRemoveConfirmGolden(t *testing.T) {
	r := newRig(t)
	r.press("down", "d")
	golden.Check(t, in{[]string{"down", "d"}, gw, gh}, r.frame())
}

func TestScreenNewPromptGolden(t *testing.T) {
	r := newRig(t)
	keys := append([]string{"n"}, typed("/no/such/dir")...)
	keys = append(keys, "enter")
	r.press(keys...)
	golden.Check(t, in{keys, gw, gh}, r.frame())
}

func TestScreenEmptyGolden(t *testing.T) {
	r := newRig(t)
	r.s = NewScreen(nil, nil, nil)
	golden.Check(t, in{[]string{"empty"}, gw, gh}, r.frame())
}

func TestEnterOpensThePaletteInThatProject(t *testing.T) {
	r := newRig(t)
	cmd := r.press("down", "enter")
	if want := [][]string{{"-C", "/work/beta"}}; !reflect.DeepEqual(r.calls, want) {
		t.Errorf("calls %v, want %v", r.calls, want)
	}
	if !cmd.Cooked || cmd.Wait || cmd.Quit {
		t.Errorf("the palette runs cooked, with no wait and no quit: %+v", cmd)
	}
}

func TestCleanupRunsTheVerbAndReloads(t *testing.T) {
	r := newRig(t)
	loaded := r.rows[:2]
	r.rows = loaded
	cmd := r.press("c")
	if want := [][]string{{"projects", "cleanup"}}; !reflect.DeepEqual(r.calls, want) {
		t.Errorf("calls %v", r.calls)
	}
	if !cmd.Wait {
		t.Error("cleanup prints, so it waits for a key")
	}
	if !strings.Contains(strings.Join(r.frame(), "\n"), "2 registered") {
		t.Error("the list was not reloaded")
	}
}

func TestRemoveAsksFirstAndActsOnTheRowThatWasSelected(t *testing.T) {
	r := newRig(t)
	r.press("down", "d", "n")
	if len(r.calls) != 0 {
		t.Fatalf("declined confirm ran %v", r.calls)
	}
	r.press("d", "esc")
	if len(r.calls) != 0 {
		t.Fatalf("cancelled confirm ran %v", r.calls)
	}
	r.press("d", "y")
	if want := [][]string{{"projects", "remove", "/work/beta"}}; !reflect.DeepEqual(r.calls, want) {
		t.Errorf("calls %v, want %v", r.calls, want)
	}
}

func TestNewRunsInitInTheTypedDirectory(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	r.press("n")
	r.press(typed(dir)...)
	r.press("enter")
	if want := [][]string{{"-C", dir, "init"}}; !reflect.DeepEqual(r.calls, want) {
		t.Errorf("calls %v, want %v", r.calls, want)
	}
}

func TestNewRejectsAMissingDirectoryAndEscCancels(t *testing.T) {
	r := newRig(t)
	r.press("n")
	r.press(typed("/no/such/dir")...)
	r.press("enter")
	if len(r.calls) != 0 {
		t.Fatalf("a bad directory ran %v", r.calls)
	}
	r.press("esc")
	r.press("q")
	if len(r.calls) != 0 {
		t.Errorf("esc must cancel without running: %v", r.calls)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, name := range []string{"q", "esc"} {
		r := newRig(t)
		if cmd := r.press(name); !cmd.Quit {
			t.Errorf("%s should leave the screen", name)
		}
	}
	// With a filter set, Esc clears it first and q types into it while filtering.
	r := newRig(t)
	r.press("/", "a")
	if cmd := r.press("esc"); cmd.Quit {
		t.Error("esc with a filter clears the filter")
	}
	if cmd := r.press("esc"); !cmd.Quit {
		t.Error("esc again leaves")
	}
	r = newRig(t)
	if cmd := r.press("/", "q"); cmd.Quit {
		t.Error("q while filtering is text")
	}
}

func TestFailedActionShowsTheErrorAndKeepsRunning(t *testing.T) {
	r := newRig(t)
	r.err = errors.New("rota projects cleanup exited 70")
	r.press("c")
	if !strings.Contains(strings.Join(r.frame(), "\n"), "error: rota projects cleanup exited 70") {
		t.Error("the failure is not shown")
	}
}
