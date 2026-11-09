package worker

import (
	"context"
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
}

func (f *arrangeFake) PaneOf(context.Context, string, string) string { return "p1" }
func (f *arrangeFake) LayoutPanes(context.Context) ([]host.LayoutPane, error) {
	return []host.LayoutPane{
		{ID: "p0", Tab: "T", Workspace: "W", TabLabel: layout.SplitLabel, Cwd: f.root, Agent: "claude"},
		{ID: "p1", Tab: "T", Workspace: "W", Cwd: f.worktree, Agent: "claude"},
	}, nil
}
func (f *arrangeFake) PaneRects(context.Context, string) (map[string]host.Rect, error) {
	return nil, nil
}
func (f *arrangeFake) SplitInto(context.Context, string, string, string, string, float64) error {
	return nil
}
func (f *arrangeFake) ToNewTab(_ context.Context, pane, _ string) error {
	f.log = append(f.log, "ToNewTab "+pane)
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
