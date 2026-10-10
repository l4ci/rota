package worker

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
)

// gridHost is a fakeHost that also arranges panes, like herdr: a spawn adds
// the new worker in a tab of its own; moves are logged.
type gridHost struct {
	*fakeHost
	panes []host.LayoutPane
	log   []string
}

func (g *gridHost) PaneOf(_ context.Context, slot, _ string) string {
	for _, p := range g.panes {
		if p.TabLabel == slot {
			return p.ID
		}
	}
	return ""
}
func (g *gridHost) LayoutPanes(context.Context) ([]host.LayoutPane, error) {
	return append([]host.LayoutPane(nil), g.panes...), nil
}
func (g *gridHost) PaneRects(context.Context, string) (map[string]host.Rect, error) {
	return map[string]host.Rect{}, nil
}
func (g *gridHost) move(pane, tab string) {
	for i := range g.panes {
		if g.panes[i].ID == pane {
			g.panes[i].Tab = tab
		}
	}
}
func (g *gridHost) SplitInto(_ context.Context, pane, tab, target, dir string, keep float64) error {
	g.log = append(g.log, fmt.Sprintf("split %s %s %s %s", pane, dir, target, host.FormatRatio(keep)))
	g.move(pane, tab)
	return nil
}
func (g *gridHost) RenameTab(_ context.Context, tab, label string) error {
	g.log = append(g.log, "rename tab "+tab+" "+label)
	for i := range g.panes {
		if g.panes[i].Tab == tab {
			g.panes[i].TabLabel = label
		}
	}
	return nil
}
func (g *gridHost) RenamePane(_ context.Context, pane, label string) error {
	g.log = append(g.log, "name pane "+pane+" "+label)
	return nil
}
func (g *gridHost) ToNewTab(_ context.Context, pane, label string) error {
	g.log = append(g.log, "tab "+pane+" "+label)
	g.move(pane, "t-"+label)
	return nil
}

// spawnedIn: Spawn also opens the worker's pane in a tab of its own.
func (g *gridHost) Spawn(ctx context.Context, o host.SpawnOpts) (string, error) {
	h, err := g.fakeHost.Spawn(ctx, o)
	g.panes = append(g.panes, host.LayoutPane{ID: "p-" + o.Slot, Tab: "t-" + o.Slot, TabLabel: o.Slot, Cwd: o.Cwd, Agent: "claude"})
	return h, err
}

func gridRig(t *testing.T, layoutMode string) (string, *gridHost, Env) {
	t.Helper()
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 2, Base: "main"})
	if layoutMode != "" {
		if err := Update(dir, func(d *Doc) { d.SetLayout(layoutMode) }); err != nil {
			t.Fatal(err)
		}
	}
	g := &gridHost{fakeHost: &fakeHost{name: "herdr", inSession: true, where: "main"}}
	g.panes = []host.LayoutPane{{ID: "p0", Tab: "T", TabLabel: "orchestrator", Cwd: dir, Agent: "claude"}}
	return dir, g, envWith(g)
}

func TestDispatchSpawnsIntoTheRecordedSplit(t *testing.T) {
	dir, g, e := gridRig(t, "split")
	res, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(g.log, "|"); got != "split p-w1 right p0 0.4|rename tab T rota|name pane p0 orchestrator" {
		t.Errorf("moves = %q", got)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings: %v", res.Warnings)
	}
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w2", BodyFile: writeBrief(t, "t"), Task: "T2"}); err != nil {
		t.Fatal(err)
	}
	// Two workers: the grid is rebuilt around both, none left in a tab.
	for _, p := range g.panes[1:] {
		if p.Tab != "T" {
			t.Errorf("%s left in tab %s", p.ID, p.Tab)
		}
	}
}

func TestDispatchInTabsMovesNothing(t *testing.T) {
	dir, g, e := gridRig(t, "")
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if len(g.log) != 0 {
		t.Errorf("tabs round moved panes: %v", g.log)
	}
	// Flipping back to tabs forgets the split.
	Update(dir, func(d *Doc) { d.SetLayout("split") })
	Update(dir, func(d *Doc) { d.SetLayout("tabs") })
	if got := LoadRegistryTolerant(dir).Layout(); got != "" {
		t.Errorf("layout = %q", got)
	}
}

func TestDispatchRelayDoesNotRearrange(t *testing.T) {
	dir, g, e := gridRig(t, "split")
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	g.log = nil
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "r"), Relay: true}); err != nil {
		t.Fatal(err)
	}
	if len(g.log) != 0 {
		t.Errorf("relay moved panes: %v", g.log)
	}
}

func TestDispatchSplitWithoutOrchestratorPaneWarns(t *testing.T) {
	dir, g, e := gridRig(t, "split")
	g.panes = nil
	res, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "t"), Task: "T1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no orchestrator pane") {
		t.Errorf("warnings: %v", res.Warnings)
	}
}
