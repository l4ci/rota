package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// layoutRig is a herdr with one project: the orchestrator's pane p0 in tab T,
// and one pane per live slot in a tab of its own. Moves are logged.
type layoutRig struct {
	panes []host.LayoutPane
	agent map[string]string // slot -> pane
	log   []string
	grid  map[string]host.Rect
}

func (r *layoutRig) PaneOf(_ context.Context, slot, _ string) string { return r.agent[slot] }
func (r *layoutRig) LayoutPanes(context.Context) ([]host.LayoutPane, error) {
	return append([]host.LayoutPane(nil), r.panes...), nil
}
func (r *layoutRig) PaneRects(context.Context, string) (map[string]host.Rect, error) {
	return r.grid, nil
}
func (r *layoutRig) setTab(pane, tab string) {
	for i := range r.panes {
		if r.panes[i].ID == pane {
			r.panes[i].Tab = tab
		}
	}
}
func (r *layoutRig) SplitInto(_ context.Context, pane, tab, target, dir string, keep float64) error {
	r.log = append(r.log, fmt.Sprintf("split %s %s %s %s", pane, dir, target, host.FormatRatio(keep)))
	r.setTab(pane, tab)
	return nil
}
func (r *layoutRig) RenameTab(_ context.Context, tab, label string) error {
	r.log = append(r.log, "rename tab "+tab+" "+label)
	for i := range r.panes {
		if r.panes[i].Tab == tab {
			r.panes[i].TabLabel = label
		}
	}
	return nil
}
func (r *layoutRig) RenamePane(_ context.Context, pane, label string) error {
	r.log = append(r.log, "name pane "+pane+" "+label)
	return nil
}
func (r *layoutRig) ToNewTab(_ context.Context, pane, label string) error {
	r.log = append(r.log, fmt.Sprintf("tab %s %s", pane, label))
	r.setTab(pane, "t-"+label)
	return nil
}

// layoutProject makes a project whose registry has the slots (all with a
// handle) and a rig with the orchestrator and, for each name in live, a pane.
func layoutProject(t *testing.T, regHost string, slots []string, live ...string) (string, *layoutRig, *Deps) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	var reg []string
	for i, s := range slots {
		reg = append(reg, fmt.Sprintf(`{"name":%q,"handle":"w1:t%d"}`, s, i+2))
	}
	doc := fmt.Sprintf(`{"host":%q,"slots":[%s]}`, regHost, strings.Join(reg, ","))
	if err := os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	rig := &layoutRig{agent: map[string]string{}}
	rig.panes = []host.LayoutPane{{ID: "p0", Tab: "T", TabLabel: "orchestrator", Cwd: root, Agent: "claude"}}
	for _, s := range live {
		id := "p-" + s
		rig.agent[s] = id
		rig.panes = append(rig.panes, host.LayoutPane{ID: id, Tab: "t-" + s, TabLabel: s, Cwd: filepath.Join(root, ".worktrees", s), Agent: "claude"})
	}
	deps := testDeps()
	deps.LayoutHost = func() (host.Layouter, error) { return rig, nil }
	return root, rig, deps
}

func layoutRows(env map[string]any) []*jsonx.Object {
	var out []*jsonx.Object
	rows, _ := umbData(env)["projects"].([]any)
	for _, r := range rows {
		out = append(out, r.(*jsonx.Object))
	}
	return out
}

func TestLayoutStatusReportsTabsSplitAndMixed(t *testing.T) {
	root, rig, deps := layoutProject(t, "herdr", []string{"ben", "dana"}, "ben", "dana")
	code, env, errs := rotaRunWith(t, deps, "--json", "layout", "--project", root)
	rows := layoutRows(env)
	if code != 0 || len(rows) != 1 || jsonx.Str(rows[0], "layout") != "tabs" {
		t.Fatalf("exit %d rows %v: %s", code, rows, errs)
	}
	rig.setTab("p-ben", "T")
	_, env, _ = rotaRunWith(t, deps, "--json", "layout", "--project", root)
	if got := jsonx.Str(layoutRows(env)[0], "layout"); got != "mixed" {
		t.Errorf("one folded: %s", got)
	}
	rig.setTab("p-dana", "T")
	_, env, _ = rotaRunWith(t, deps, "--json", "layout", "--project", root)
	if got := jsonx.Str(layoutRows(env)[0], "layout"); got != "split" {
		t.Errorf("both folded: %s", got)
	}
	if len(rig.log) != 0 {
		t.Errorf("status moved panes: %v", rig.log)
	}
}

func TestLayoutSplitThenTabs(t *testing.T) {
	root, rig, deps := layoutProject(t, "herdr", []string{"ben", "dana", "nia"}, "ben", "dana", "nia")
	code, env, errs := rotaRunWith(t, deps, "--json", "layout", "split", "--project", root)
	rows := layoutRows(env)
	if code != 0 || jsonx.Str(rows[0], "layout") != "split" || jsonx.Str(rows[0], "before") != "tabs" {
		t.Fatalf("exit %d rows %v: %s", code, rows, errs)
	}
	want := "split p-ben right p0 0.4|split p-nia right p-ben 0.5|split p-dana down p-ben 0.5|rename tab T rota|name pane p0 orchestrator"
	if got := strings.Join(rig.log, "|"); got != want {
		t.Errorf("moves = %s", got)
	}
	ws, _ := rows[0].Get("workers")
	if tab := jsonx.Str(ws.([]any)[2].(*jsonx.Object), "tab"); tab != "T" {
		t.Errorf("the report shows the panes where they were, not where they are: tab %q", tab)
	}
	rig.log = nil
	code, env, _ = rotaRunWith(t, deps, "--json", "layout", "tabs", "--project", root)
	if code != 0 || jsonx.Str(layoutRows(env)[0], "layout") != "tabs" {
		t.Fatalf("tabs: exit %d %v", code, env)
	}
	if got := strings.Join(rig.log, "|"); got != "tab p-ben ben|tab p-dana dana|tab p-nia nia|rename tab T orchestrator" {
		t.Errorf("moves = %s", got)
	}
}

// Run from the pane that launched the round, split puts that pane on top of the
// orchestrator in its own tab, and remembers it for later spawns.
func TestLayoutSplitKeepsTheLaunchingCLIOnTop(t *testing.T) {
	root, rig, deps := layoutProject(t, "herdr", []string{"ben"}, "ben")
	rig.panes = append(rig.panes, host.LayoutPane{ID: "pc", Tab: "tc", TabLabel: "zsh", Cwd: root})
	t.Setenv("HERDR_PANE_ID", "pc")
	code, env, errs := rotaRunWith(t, deps, "--json", "-C", root, "layout", "split")
	rows := layoutRows(env)
	if code != 0 || jsonx.Str(rows[0], "layout") != "split" {
		t.Fatalf("exit %d rows %v: %s", code, rows, errs)
	}
	want := "split p-ben right pc 0.4|split p0 down pc 0.25|rename tab tc rota|name pane p0 orchestrator"
	if got := strings.Join(rig.log, "|"); got != want {
		t.Errorf("moves = %s", got)
	}
	if got := worker.LoadRegistry(root).CLIPane(); got != "pc" {
		t.Errorf("recorded cli pane = %q", got)
	}
	cli, _ := rows[0].Get("cli")
	if jsonx.Str(cli.(*jsonx.Object), "pane") != "pc" {
		t.Errorf("cli = %v", cli)
	}
	// From the orchestrator (an agent pane) the recorded CLI is still used,
	// and tabs puts the orchestrator back in a tab of its own.
	t.Setenv("HERDR_PANE_ID", "p0")
	rig.log = nil
	if code, _, errs := rotaRunWith(t, deps, "--json", "-C", root, "layout", "tabs"); code != 0 {
		t.Fatalf("tabs: exit %d: %s", code, errs)
	}
	if got := strings.Join(rig.log, "|"); got != "tab p-ben ben|tab p0 orchestrator" {
		t.Errorf("tabs moves = %s", got)
	}
}

func TestLayoutSkipsParkedAndDeadSlots(t *testing.T) {
	// dana has a handle but no agent (dead); kit has neither (parked).
	root, rig, deps := layoutProject(t, "herdr", []string{"ben", "dana", "kit"}, "ben")
	if code, _, errs := rotaRunWith(t, deps, "--json", "layout", "split", "--project", root); code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got := strings.Join(rig.log, "|"); got != "split p-ben right p0 0.4|rename tab T rota|name pane p0 orchestrator" {
		t.Errorf("moves = %s", got)
	}
}

func TestLayoutWarnsAboutAForeignPane(t *testing.T) {
	root, rig, deps := layoutProject(t, "herdr", []string{"ben"}, "ben")
	rig.panes = append(rig.panes, host.LayoutPane{ID: "sh", Tab: "T", Cwd: root})
	code, _, errs := rotaRunWith(t, deps, "--json", "layout", "split", "--project", root)
	if code != 0 || !strings.Contains(errs, "pane sh in the orchestrator tab is not rota's") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
	for _, l := range rig.log {
		if strings.Contains(l, " sh ") {
			t.Errorf("foreign pane moved: %s", l)
		}
	}
}

func TestLayoutTmuxAndSoloRoundsAreSkipped(t *testing.T) {
	for _, h := range []string{"tmux", "solo"} {
		root, rig, deps := layoutProject(t, h, []string{"ben"}, "ben")
		code, env, errs := rotaRunWith(t, deps, "--json", "layout", "split", "--project", root)
		if code != ExitUnavailable || !strings.Contains(errs, "host is "+h+": layout needs herdr") {
			t.Errorf("%s: exit %d, stderr %q", h, code, errs)
		}
		if len(rig.log) != 0 {
			t.Errorf("%s: moved panes: %v", h, rig.log)
		}
		_ = env
	}
	// Bare status exits 0 and names the skip.
	root, _, deps := layoutProject(t, "tmux", []string{"ben"}, "ben")
	code, env, _ := rotaRunWith(t, deps, "--json", "layout", "--project", root)
	if code != 0 || jsonx.Str(layoutRows(env)[0], "skipped") != "host is tmux: layout needs herdr" {
		t.Errorf("status: exit %d %v", code, env)
	}
}

func TestLayoutAllProjectsDefaultsToTheOnesOpenInHerdr(t *testing.T) {
	root, _, deps := layoutProject(t, "herdr", []string{"ben"}, "ben")
	// From inside the project, with nothing registered: found by the cwd.
	code, env, errs := rotaRunWith(t, deps, "--json", "-C", root, "layout", "split")
	if code != 0 || len(layoutRows(env)) != 1 {
		t.Fatalf("exit %d %v: %s", code, env, errs)
	}
	// A project with no orchestrator pane in herdr is not listed, and with
	// nothing arranged the verb says so.
	other, _, deps2 := layoutProject(t, "herdr", []string{"ben"}, "ben")
	deps2.LayoutHost = func() (host.Layouter, error) { return &layoutRig{agent: map[string]string{}}, nil }
	code, _, errs = rotaRunWith(t, deps2, "--json", "-C", other, "layout", "split")
	if code != ExitResolution || !strings.Contains(errs, "no rota project has a round open in herdr") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
}

func TestLayoutWithoutHerdrIsUnavailable(t *testing.T) {
	root, _, _ := layoutProject(t, "herdr", []string{"ben"}, "ben")
	code, _, errs := rotaRunWith(t, testDeps(), "--json", "layout", "split", "--project", root)
	if code != ExitUnavailable || !strings.Contains(errs, "herdr is not installed") {
		t.Errorf("exit %d, stderr %q", code, errs)
	}
}

func TestLayoutRefusesADirectoryWithoutRota(t *testing.T) {
	_, _, deps := layoutProject(t, "herdr", nil)
	code, _, _ := rotaRunWith(t, deps, "--json", "layout", "--project", t.TempDir())
	if code != ExitResolution {
		t.Errorf("exit %d", code)
	}
}

// #205: split and tabs are remembered on the round, so workers assigned later
// join the grid; the bare report does not change it.
func TestLayoutSplitAndTabsRecordOnTheRound(t *testing.T) {
	root, _, deps := layoutProject(t, "herdr", []string{"ben"}, "ben")
	if got := worker.LoadRegistry(root).Layout(); got != "" {
		t.Fatalf("fresh round layout = %q", got)
	}
	rotaRunWith(t, deps, "--json", "layout", "split", "--project", root)
	if got := worker.LoadRegistry(root).Layout(); got != "split" {
		t.Errorf("after split: %q", got)
	}
	rotaRunWith(t, deps, "--json", "layout", "--project", root)
	if got := worker.LoadRegistry(root).Layout(); got != "split" {
		t.Errorf("report changed it: %q", got)
	}
	rotaRunWith(t, deps, "--json", "layout", "tabs", "--project", root)
	if got := worker.LoadRegistry(root).Layout(); got != "" {
		t.Errorf("after tabs: %q", got)
	}
}
