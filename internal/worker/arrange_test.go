package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/layout"
)

// arrangeFake is a herdr that holds one split round: the orchestrator and slot
// w1 share a tab. It is handed to ArrangeRound only through Capabilities.
type arrangeFake struct {
	root, worktree string
	log            []string
	moved          map[string]string // pane -> tab it was moved to
	cli            bool              // a plain shell pane "pc" sits beside the round
	failMove       error             // ToNewTab fails with this
}

func (f *arrangeFake) PaneOf(context.Context, string, string) string { return "p1" }
func (f *arrangeFake) LayoutPanes(context.Context) ([]host.LayoutPane, error) {
	panes := []host.LayoutPane{
		{ID: "p0", Tab: "T", Workspace: "W", TabLabel: layout.SplitLabel, Cwd: f.root, Agent: "claude"},
		{ID: "p1", Tab: "T", Workspace: "W", Cwd: f.worktree, Agent: "claude"},
	}
	if f.cli {
		panes = append(panes, host.LayoutPane{ID: "pc", Tab: "tc", Workspace: "W", Cwd: f.root})
	}
	for i := range panes {
		if tab, ok := f.moved[panes[i].ID]; ok {
			panes[i].Tab = tab
		}
	}
	return panes, nil
}
func (f *arrangeFake) PaneRects(context.Context, string) (map[string]host.Rect, error) {
	return nil, nil
}
func (f *arrangeFake) SplitInto(context.Context, string, string, string, string, float64) error {
	return nil
}
func (f *arrangeFake) ToNewTab(_ context.Context, pane, _ string) error {
	f.log = append(f.log, "ToNewTab "+pane)
	if f.failMove != nil {
		return f.failMove
	}
	if f.moved == nil {
		f.moved = map[string]string{}
	}
	f.moved[pane] = "T-" + pane
	return nil
}
func (f *arrangeFake) RenameTab(context.Context, string, string) error  { return nil }
func (f *arrangeFake) RenamePane(context.Context, string, string) error { return nil }
func (f *arrangeFake) OpenLinked(_ context.Context, _, slot, _ string) (string, string, error) {
	f.log = append(f.log, "OpenLinked "+slot)
	return "ws-" + slot, "", nil
}
func (f *arrangeFake) ToNewTabIn(_ context.Context, pane, ws, _ string) error {
	f.log = append(f.log, "ToNewTabIn "+pane+" "+ws)
	return nil
}
func (f *arrangeFake) CloseTab(context.Context, string) error       { return nil }
func (f *arrangeFake) CloseWorkspace(context.Context, string) error { return nil }
func (f *arrangeFake) caps(group bool) host.Capabilities {
	c := host.Capabilities{Layouter: f}
	if group {
		c.LinkedLayouter, c.WorkspaceCloser = f, f
	}
	return c
}

// Grouping is decided once, from the capabilities: the same fake lands the slot
// in a linked workspace when the host can group and in a plain tab when it
// cannot, and the layouter comes from the capabilities alone.
func TestArrangeRoundDecidesGroupingFromCapabilities(t *testing.T) {
	cases := []struct {
		name  string
		group bool // the host can group
		cfg   string
		want  string
	}{
		{"host can group", true, `{"round":{"herdrGroupSlots":true}}`, "OpenLinked w1"},
		{"host cannot group", false, `{"round":{"herdrGroupSlots":true}}`, "ToNewTab p1"},
		{"config off", true, `{"round":{"herdrGroupSlots":false}}`, "ToNewTab p1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
			reg, err := LoadRegistry(dir)
			if err != nil {
				t.Fatal(err)
			}
			f := &arrangeFake{root: dir, worktree: reg.Slots()[0].Worktree()}
			v, err := jsonx.Decode([]byte(c.cfg))
			if err != nil {
				t.Fatal(err)
			}
			a, err := ArrangeRound(bg, f.caps(c.group), ParseHostConfig(v), dir, layout.Tabs, "", reg)
			if err != nil || !a.Found {
				t.Fatalf("found=%v err=%v", a.Found, err)
			}
			if got := strings.Join(f.log, "\n"); !strings.Contains(got, c.want) {
				t.Errorf("log = %q, want %q", got, c.want)
			}
			if a.Grouped != strings.HasPrefix(c.want, "OpenLinked") {
				t.Errorf("Grouped = %v", a.Grouped)
			}
		})
	}
	if _, err := ArrangeRound(bg, host.Capabilities{}, HostConfig{}, "", layout.Tabs, "", Registry{}); err == nil {
		t.Error("a host with no Layouter must be refused")
	}
}

// ArrangeRound owns the bookkeeping around a move: it returns where the panes
// are after it, and records the layout mode on the round.
func TestArrangeRoundReturnsPostMoveStateAndRecordsLayout(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	reg, err := LoadRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := &arrangeFake{root: dir, worktree: reg.Slots()[0].Worktree()}
	cfg := ParseHostConfig(nil)

	a, err := ArrangeRound(bg, f.caps(false), cfg, dir, layout.Tabs, "", reg)
	if err != nil || !a.Found {
		t.Fatalf("found=%v err=%v", a.Found, err)
	}
	if a.Before != layout.Split {
		t.Errorf("Before = %q, want the split it started in", a.Before)
	}
	if len(a.State.Workers) != 1 || a.State.Workers[0].Tab != "T-p1" {
		t.Errorf("State = %+v, want w1 in its new tab T-p1 (post-move)", a.State.Workers)
	}
	if a.LayoutErr != nil {
		t.Errorf("LayoutErr = %v", a.LayoutErr)
	}
	if got := LoadRegistryTolerant(dir).Layout(); got != "" {
		t.Errorf("tabs recorded layout %q, want the default", got)
	}

	a, err = ArrangeRound(bg, f.caps(false), cfg, dir, layout.Split, "", reg)
	if err != nil || !a.Found {
		t.Fatalf("split: found=%v err=%v", a.Found, err)
	}
	if got := LoadRegistryTolerant(dir).Layout(); got != layout.Split {
		t.Errorf("split recorded layout %q", got)
	}
}

func TestArrangeRoundRecordsNothingWhenNoOrchestratorIsFound(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	reg, err := LoadRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	f := &arrangeFake{root: t.TempDir(), worktree: reg.Slots()[0].Worktree()}
	a, err := ArrangeRound(bg, f.caps(false), ParseHostConfig(nil), dir, layout.Split, "", reg)
	if err != nil || a.Found {
		t.Fatalf("found=%v err=%v", a.Found, err)
	}
	if got := LoadRegistryTolerant(dir).Layout(); got != "" {
		t.Errorf("layout %q recorded with nothing arranged", got)
	}
}

func arrangeRig(t *testing.T) (string, Registry, *arrangeFake) {
	t.Helper()
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	reg, err := LoadRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, reg, &arrangeFake{root: dir, worktree: reg.Slots()[0].Worktree()}
}

func TestArrangeRoundRecordsTheCLIPaneOnSplit(t *testing.T) {
	dir, reg, f := arrangeRig(t)
	f.cli = true
	if _, err := ArrangeRound(bg, f.caps(false), ParseHostConfig(nil), dir, layout.Split, "pc", reg); err != nil {
		t.Fatal(err)
	}
	if got := LoadRegistryTolerant(dir).CLIPane(); got != "pc" {
		t.Errorf("CLIPane = %q, want pc", got)
	}
}

func TestArrangeRoundKeepsTheRecordedLayoutWhenAMoveFails(t *testing.T) {
	dir, reg, f := arrangeRig(t)
	if err := Update(dir, func(d *Doc) { d.SetLayout(layout.Split) }); err != nil {
		t.Fatal(err)
	}
	f.failMove = errors.New("herdr refused")
	a, err := ArrangeRound(bg, f.caps(false), ParseHostConfig(nil), dir, layout.Tabs, "", reg)
	if err == nil {
		t.Fatal("want the move failure")
	}
	if a.LayoutErr != nil {
		t.Errorf("LayoutErr = %v", a.LayoutErr)
	}
	if got := LoadRegistryTolerant(dir).Layout(); got != layout.Split {
		t.Errorf("layout = %q after a failed move, want split kept", got)
	}
}

func TestArrangeRoundForASpawnLeavesLayoutAndCLIPaneAlone(t *testing.T) {
	dir, reg, f := arrangeRig(t)
	f.cli = true
	if _, err := arrangeRound(bg, f.caps(false), ParseHostConfig(nil), dir, layout.Split, "pc", reg, false); err != nil {
		t.Fatal(err)
	}
	if r := LoadRegistryTolerant(dir); r.Layout() != "" || r.CLIPane() != "" {
		t.Errorf("layout=%q cliPane=%q recorded by a re-split", r.Layout(), r.CLIPane())
	}
}

func TestShowRoundMovesAndWritesNothing(t *testing.T) {
	dir, reg, f := arrangeRig(t)
	f.cli = true
	panes, _ := f.LayoutPanes(bg)
	before, _ := os.ReadFile(filepath.Join(dir, ".rota", "workers.json"))
	st, ok := ShowRound(bg, f, panes, dir, "pc", reg)
	if !ok || st.CLI == nil || len(st.Workers) != 1 {
		t.Fatalf("ok=%v state=%+v", ok, st)
	}
	after, _ := os.ReadFile(filepath.Join(dir, ".rota", "workers.json"))
	if string(before) != string(after) || len(f.log) != 0 {
		t.Errorf("show wrote or moved: log=%v", f.log)
	}
}
