package layout

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
)

var bg = context.Background()

// TestMain strips the host environment: a test must never reach a live herdr.
func TestMain(m *testing.M) {
	for _, k := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_WORKSPACE_ID", "HERDR_PANE_ID", "HERDR_TAB_ID", "HERDR_SOCKET_PATH"} {
		os.Unsetenv(k)
	}
	os.Exit(m.Run())
}

const root = "/p"

// fakeHerdr tracks which tab each pane is in and logs every move as the herdr
// command that carries it out.
type fakeHerdr struct {
	tab   map[string]string // pane -> tab
	rects map[string]host.Rect
	log   []string
	next  int
}

func (f *fakeHerdr) PaneOf(context.Context, string, string) string { return "" }

func (f *fakeHerdr) LayoutPanes(context.Context) ([]host.LayoutPane, error) {
	return nil, nil
}

func (f *fakeHerdr) PaneRects(context.Context, string) (map[string]host.Rect, error) {
	return f.rects, nil
}

func (f *fakeHerdr) SplitInto(_ context.Context, pane, tab, target, dir string, keep float64) error {
	f.log = append(f.log, fmt.Sprintf("pane move %s --tab %s --split %s --target-pane %s --ratio %s --no-focus", pane, tab, dir, target, host.FormatRatio(keep)))
	f.tab[pane] = tab
	return nil
}

func (f *fakeHerdr) RenameTab(_ context.Context, tab, label string) error {
	f.log = append(f.log, "rename tab "+tab+" "+label)
	return nil
}

func (f *fakeHerdr) RenamePane(_ context.Context, pane, label string) error {
	f.log = append(f.log, "name pane "+pane+" "+label)
	return nil
}

func (f *fakeHerdr) ToNewTab(_ context.Context, pane, label string) error {
	f.log = append(f.log, fmt.Sprintf("pane move %s --new-tab --label %s --no-focus", pane, label))
	f.next++
	f.tab[pane] = fmt.Sprintf("new%d", f.next)
	return nil
}

// round builds n workers s1..sn (panes p1..pn) in tabs of their own beside the
// orchestrator (pane p0, tab T), as `round assign` leaves them.
func round(n int) (*fakeHerdr, []host.LayoutPane, []Worker) {
	f := &fakeHerdr{tab: map[string]string{}}
	panes := []host.LayoutPane{{ID: "p0", Tab: "T", TabLabel: "orchestrator", Cwd: root, Agent: "claude"}}
	f.tab["p0"] = "T"
	var ws []Worker
	for i := 1; i <= n; i++ {
		id, slot := fmt.Sprintf("p%d", i), fmt.Sprintf("s%d", i)
		panes = append(panes, host.LayoutPane{ID: id, Tab: "t" + id, TabLabel: slot, Cwd: root + "/.worktrees/" + slot, Agent: "claude"})
		f.tab[id] = "t" + id
		ws = append(ws, Worker{Slot: slot, Pane: id})
	}
	return f, panes, ws
}

func state(t *testing.T, f *fakeHerdr, panes []host.LayoutPane, ws []Worker, cli string) State {
	t.Helper()
	cur := make([]host.LayoutPane, len(panes))
	copy(cur, panes)
	for i := range cur {
		cur[i].Tab = f.tab[cur[i].ID]
	}
	st, ok := Find(cur, root, "orchestrator", cli, ws)
	if !ok {
		t.Fatal("orchestrator not found")
	}
	return st
}

func mv(pane, dir, target, ratio string) string {
	return fmt.Sprintf("pane move %s --tab T --split %s --target-pane %s --ratio %s --no-focus", pane, dir, target, ratio)
}

// renames are what a finished split also does: label the tab, name the pane.
var renames = []string{"rename tab T rota", "name pane p0 orchestrator"}

func TestSplitCommandSequence(t *testing.T) {
	cases := map[int][]string{
		0: nil,
		1: {mv("p1", "right", "p0", "0.4")},
		2: {mv("p1", "right", "p0", "0.4"), mv("p2", "down", "p1", "0.5")},
		3: {mv("p1", "right", "p0", "0.4"), mv("p3", "right", "p1", "0.5"), mv("p2", "down", "p1", "0.5")},
		4: {mv("p1", "right", "p0", "0.4"), mv("p3", "right", "p1", "0.5"), mv("p2", "down", "p1", "0.5"), mv("p4", "down", "p3", "0.5")},
		5: {mv("p1", "right", "p0", "0.4"), mv("p3", "right", "p1", "0.5"), mv("p5", "right", "p3", "0.5"),
			mv("p2", "down", "p1", "0.5"), mv("p4", "down", "p3", "0.5")},
	}
	// Three worker columns: the second pane keeps a third of the 60%.
	cases[5][1] = mv("p3", "right", "p1", "0.3333")
	for n, want := range cases {
		f, panes, ws := round(n)
		res, err := ToSplit(bg, f, state(t, f, panes, ws, ""))
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if n > 0 {
			want = append(want, renames...)
		}
		if !reflect.DeepEqual(f.log, want) {
			t.Errorf("n=%d commands:\n%s\nwant:\n%s", n, strings.Join(f.log, "\n"), strings.Join(want, "\n"))
		}
		if res.Moves != len(want)-len(renames)*b2i(n > 0) {
			t.Errorf("n=%d moves = %d", n, res.Moves)
		}
		wantAfter := Split
		if n == 0 {
			wantAfter = Tabs
		}
		if got := state(t, f, panes, ws, "").Layout(); got != wantAfter {
			t.Errorf("n=%d layout = %s, want %s", n, got, wantAfter)
		}
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// roundCLI is round(n) plus the pane that launched it: a clean shell "c" in a
// tab of its own.
func roundCLI(n int) (*fakeHerdr, []host.LayoutPane, []Worker) {
	f, panes, ws := round(n)
	panes = append(panes, host.LayoutPane{ID: "c", Tab: "tc", TabLabel: "zsh", Cwd: root})
	f.tab["c"] = "tc"
	return f, panes, ws
}

func mvIn(tab, pane, dir, target, ratio string) string {
	return fmt.Sprintf("pane move %s --tab %s --split %s --target-pane %s --ratio %s --no-focus", pane, tab, dir, target, ratio)
}

// With the launching pane known the split lives in its tab: the worker
// columns are cut first, so the left column is still one full-height pane,
// then the orchestrator goes under the CLI at a quarter of that column.
func TestSplitWithCLIPutsTheOrchestratorUnderIt(t *testing.T) {
	f, panes, ws := roundCLI(3)
	st := state(t, f, panes, ws, "c")
	if st.CLI == nil || st.Layout() != Tabs {
		t.Fatalf("cli = %v layout = %s", st.CLI, st.Layout())
	}
	res, err := ToSplit(bg, f, st)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		mvIn("tc", "p1", "right", "c", "0.4"), mvIn("tc", "p3", "right", "p1", "0.5"), mvIn("tc", "p2", "down", "p1", "0.5"),
		mvIn("tc", "p0", "down", "c", "0.25"),
		"rename tab tc rota", "name pane p0 orchestrator",
	}
	if !reflect.DeepEqual(f.log, want) || res.Moves != 4 || res.After != Split {
		t.Errorf("commands:\n%s\nresult %+v", strings.Join(f.log, "\n"), res)
	}
	if got := state(t, f, panes, ws, "c").Layout(); got != Split {
		t.Errorf("layout = %s", got)
	}
}

// A split view that already has the CLI over the orchestrator is left alone.
func TestSplitWithCLIIsIdempotent(t *testing.T) {
	f, panes, ws := roundCLI(3)
	if _, err := ToSplit(bg, f, state(t, f, panes, ws, "c")); err != nil {
		t.Fatal(err)
	}
	f.log = nil
	st := state(t, f, panes, ws, "c")
	st.Orch.TabLabel = SplitLabel
	f.rects = gridRects(st)
	res, err := ToSplit(bg, f, st)
	if err != nil || res.Moves != 0 || len(f.log) != 0 {
		t.Errorf("re-split moved or renamed: %+v %v %v", res, err, f.log)
	}
}

// Tabs puts the orchestrator back in a tab of its own and leaves the CLI where
// it is.
func TestTabsWithCLIEjectsTheOrchestratorAndWorkers(t *testing.T) {
	f, panes, ws := roundCLI(2)
	if _, err := ToSplit(bg, f, state(t, f, panes, ws, "c")); err != nil {
		t.Fatal(err)
	}
	f.log = nil
	res, err := ToTabs(bg, f, state(t, f, panes, ws, "c"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pane move p1 --new-tab --label s1 --no-focus",
		"pane move p2 --new-tab --label s2 --no-focus",
		"pane move p0 --new-tab --label orchestrator --no-focus",
	}
	if !reflect.DeepEqual(f.log, want) || res.After != Tabs {
		t.Errorf("commands:\n%s", strings.Join(f.log, "\n"))
	}
	if got := state(t, f, panes, ws, "c"); got.Layout() != Tabs || got.Orch.Tab == got.CLI.Tab {
		t.Errorf("layout = %s, orchestrator tab %s, cli tab %s", got.Layout(), got.Orch.Tab, got.CLI.Tab)
	}
}

// Without a CLI the orchestrator's own tab is the split, and tabs takes the
// name rota back off it.
func TestTabsRenamesASplitTabBack(t *testing.T) {
	f, panes, ws := round(1)
	panes[0].TabLabel = SplitLabel
	if _, err := ToTabs(bg, f, state(t, f, panes, ws, "")); err != nil {
		t.Fatal(err)
	}
	if want := []string{"rename tab T orchestrator"}; !reflect.DeepEqual(f.log, want) {
		t.Errorf("commands: %v", f.log)
	}
}

// A CLI pane that is gone, or is the orchestrator, is not used.
func TestFindIgnoresAStaleOrWrongCLI(t *testing.T) {
	f, panes, ws := round(1)
	for _, cli := range []string{"nope", "p0", "p1"} {
		if st := state(t, f, panes, ws, cli); st.CLI != nil {
			t.Errorf("cli %q accepted: %+v", cli, st.CLI)
		}
	}
}

// The left column keeps 40%; each later right split leaves the target 1/(worker
// columns still to place) of the room it had, so the columns share the other
// 60% equally.
func TestSplitColumnsComeOutEqual(t *testing.T) {
	for n := 1; n <= 8; n++ {
		f, panes, ws := round(n)
		if _, err := ToSplit(bg, f, state(t, f, panes, ws, "")); err != nil {
			t.Fatal(err)
		}
		cols := (n + 1) / 2
		share := map[string]float64{"p0": 1}
		room := 1.0
		prev := "p0"
		for _, line := range f.log {
			var pane, dir, target, ratio string
			fmt.Sscanf(strings.NewReplacer("--tab T ", "", "--split ", "", "--target-pane ", "", "--ratio ", "").Replace(line), "pane move %s %s %s %s", &pane, &dir, &target, &ratio)
			if dir != "right" {
				continue
			}
			var r float64
			fmt.Sscanf(ratio, "%f", &r)
			kept := room * r
			share[prev] = kept
			room -= kept
			share[pane] = room
			prev = pane
		}
		for p, w := range share {
			want := 0.6 / float64(cols)
			if p == "p0" {
				want = 0.4
			}
			if d := w - want; d > 0.001 || d < -0.001 {
				t.Errorf("n=%d: %s gets %.4f, want %.4f", n, p, w, want)
			}
		}
	}
}

func TestTabsGivesEachWorkerItsOwnTabInSlotOrder(t *testing.T) {
	f, panes, ws := round(3)
	if _, err := ToSplit(bg, f, state(t, f, panes, ws, "")); err != nil {
		t.Fatal(err)
	}
	f.log = nil
	res, err := ToTabs(bg, f, state(t, f, panes, ws, ""))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pane move p1 --new-tab --label s1 --no-focus",
		"pane move p2 --new-tab --label s2 --no-focus",
		"pane move p3 --new-tab --label s3 --no-focus",
	}
	if !reflect.DeepEqual(f.log, want) || res.Moves != 3 || res.After != Tabs {
		t.Errorf("commands %v, result %+v", f.log, res)
	}
	st := state(t, f, panes, ws, "")
	if st.Layout() != Tabs {
		t.Errorf("layout = %s", st.Layout())
	}
	// Tabs on tabs does nothing.
	f.log = nil
	if res, err := ToTabs(bg, f, st); err != nil || res.Moves != 0 || len(f.log) != 0 {
		t.Errorf("second tabs: %+v %v %v", res, err, f.log)
	}
}

func cliID(st State) string {
	if st.CLI != nil {
		return st.CLI.ID
	}
	return ""
}

// gridRects is where herdr puts the panes of a finished split.
func gridRects(st State) map[string]host.Rect {
	out := map[string]host.Rect{}
	for x, col := range Grid(cliID(st), st.Orch.ID, st.Workers) {
		for y, id := range col {
			out[id] = host.Rect{X: x * 10, Y: y * 20, W: 10, H: 20}
		}
	}
	return out
}

func TestSplitIsIdempotent(t *testing.T) {
	f, panes, ws := round(4)
	if _, err := ToSplit(bg, f, state(t, f, panes, ws, "")); err != nil {
		t.Fatal(err)
	}
	f.log = nil
	st := state(t, f, panes, ws, "")
	st.Orch.TabLabel = SplitLabel // already named by the first split
	f.rects = gridRects(st)
	res, err := ToSplit(bg, f, st)
	if err != nil || res.Moves != 0 || len(f.log) != 0 {
		t.Errorf("re-split of a split view moved panes: %+v %v %v", res, err, f.log)
	}
}

func TestResplitFoldsInANewWorkerByRebuilding(t *testing.T) {
	f, panes, ws := round(2)
	if _, err := ToSplit(bg, f, state(t, f, panes, ws, "")); err != nil {
		t.Fatal(err)
	}
	// A third worker was assigned and opened as a tab.
	f2, panes3, ws3 := round(3)
	f2.tab = f.tab
	f2.tab["p3"] = "tp3"
	f2.log = nil
	st := state(t, f2, panes3, ws3, "")
	if st.Layout() != Mixed {
		t.Fatalf("layout = %s", st.Layout())
	}
	if _, err := ToSplit(bg, f2, st); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pane move p1 --new-tab --label s1 --no-focus",
		"pane move p2 --new-tab --label s2 --no-focus",
		mv("p1", "right", "p0", "0.4"), mv("p3", "right", "p1", "0.5"), mv("p2", "down", "p1", "0.5"),
		"rename tab T rota", "name pane p0 orchestrator",
	}
	if !reflect.DeepEqual(f2.log, want) {
		t.Errorf("commands:\n%s", strings.Join(f2.log, "\n"))
	}
}

func TestForeignPaneStaysAndIsListed(t *testing.T) {
	f, panes, ws := round(1)
	panes = append(panes, host.LayoutPane{ID: "sh", Tab: "T", Cwd: root}) // a shell the person opened
	f.tab["sh"] = "T"
	st := state(t, f, panes, ws, "")
	if len(st.Foreign) != 1 || st.Foreign[0].ID != "sh" {
		t.Fatalf("foreign = %+v", st.Foreign)
	}
	if _, err := ToSplit(bg, f, st); err != nil {
		t.Fatal(err)
	}
	if _, err := ToTabs(bg, f, state(t, f, panes, ws, "")); err != nil {
		t.Fatal(err)
	}
	for _, l := range f.log {
		if strings.Contains(l, "move sh ") {
			t.Errorf("foreign pane was moved: %s", l)
		}
	}
	if f.tab["sh"] != "T" {
		t.Errorf("foreign pane left its tab: %s", f.tab["sh"])
	}
}

func TestFindPrefersTheOrchestratorTabAndSkipsWorkerPanes(t *testing.T) {
	panes := []host.LayoutPane{
		{ID: "sh", Tab: "t9", Cwd: root},                                                 // a shell in the project
		{ID: "w", Tab: "t2", Cwd: root, Agent: "claude"},                                 // a worker whose cwd is the root
		{ID: "o", Tab: "t1", TabLabel: "orchestrator", Cwd: root + "/", Agent: "claude"}, // the orchestrator
	}
	st, ok := Find(panes, root, "orchestrator", "", []Worker{{Slot: "ben", Pane: "w"}})
	if !ok || st.Orch.ID != "o" {
		t.Fatalf("orchestrator = %+v ok=%v", st.Orch, ok)
	}
	if _, ok := Find(panes[:1], "/elsewhere", "orchestrator", "", nil); ok {
		t.Error("a pane in another project is not this project's orchestrator")
	}
}

func TestLayoutClassification(t *testing.T) {
	f, panes, ws := round(3)
	if got := state(t, f, panes, ws, "").Layout(); got != Tabs {
		t.Errorf("all in tabs = %s", got)
	}
	f.tab["p1"] = "T"
	if got := state(t, f, panes, ws, "").Layout(); got != Mixed {
		t.Errorf("one folded = %s", got)
	}
	f.tab["p2"], f.tab["p3"] = "T", "T"
	if got := state(t, f, panes, ws, "").Layout(); got != Split {
		t.Errorf("all folded = %s", got)
	}
	if got := state(t, f, panes, nil, "").Layout(); got != Tabs {
		t.Errorf("orchestrator alone = %s", got)
	}
}

// The real herdr host turns the same calls into these exact commands.
func TestSplitOverTheHerdrHost(t *testing.T) {
	var calls []string
	run := func(_ context.Context, name string, args []string) (host.Result, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return host.Result{}, nil
	}
	h := host.New("herdr", host.Deps{Run: run}).(host.Layouter)
	f, panes, ws := round(3)
	if _, err := ToSplit(bg, h, state(t, f, panes, ws, "")); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"herdr pane move p1 --tab T --split right --target-pane p0 --ratio 0.4 --no-focus",
		"herdr pane move p3 --tab T --split right --target-pane p1 --ratio 0.5 --no-focus",
		"herdr pane move p2 --tab T --split down --target-pane p1 --ratio 0.5 --no-focus",
		"herdr tab rename T rota",
		"herdr pane rename p0 orchestrator",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("commands:\n%s", strings.Join(calls, "\n"))
	}
}
