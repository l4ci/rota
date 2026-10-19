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
	cliHost
	panes []host.LayoutPane
	agent map[string]string // slot -> pane
	log   []string
	grid  map[string]host.Rect
	// failIn is a slot whose move into its linked workspace fails.
	failIn string
	// cannotLink is a slot whose `worktree open` herdr does not support.
	cannotLink string
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
		reg = append(reg, fmt.Sprintf(`{"name":%q,"handle":"w1:t%d","worktree":%q}`, s, i+2, filepath.Join(root, ".worktrees", s)))
	}
	doc := fmt.Sprintf(`{"host":%q,"slots":[%s]}`, regHost, strings.Join(reg, ","))
	if err := os.WriteFile(filepath.Join(root, ".rota", "workers.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	rig := &layoutRig{cliHost: cliHost{herdr: true}, agent: map[string]string{}}
	rig.panes = []host.LayoutPane{{ID: "p0", Tab: "T", TabLabel: "orchestrator", Cwd: root, Agent: "claude"}}
	for _, s := range live {
		id := "p-" + s
		rig.agent[s] = id
		rig.panes = append(rig.panes, host.LayoutPane{ID: id, Tab: "t-" + s, TabLabel: s, Cwd: filepath.Join(root, ".worktrees", s), Agent: "claude"})
	}
	deps := testDeps()
	deps.Host = func(host.Kind) host.Host { return rig }
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
	if got := worker.LoadRegistryTolerant(root).CLIPane(); got != "pc" {
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
	empty := &layoutRig{cliHost: cliHost{herdr: true}, agent: map[string]string{}}
	deps2.Host = func(host.Kind) host.Host { return empty }
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
	if got := worker.LoadRegistryTolerant(root).Layout(); got != "" {
		t.Fatalf("fresh round layout = %q", got)
	}
	rotaRunWith(t, deps, "--json", "layout", "split", "--project", root)
	if got := worker.LoadRegistryTolerant(root).Layout(); got != "split" {
		t.Errorf("after split: %q", got)
	}
	rotaRunWith(t, deps, "--json", "layout", "--project", root)
	if got := worker.LoadRegistryTolerant(root).Layout(); got != "split" {
		t.Errorf("report changed it: %q", got)
	}
	rotaRunWith(t, deps, "--json", "layout", "tabs", "--project", root)
	if got := worker.LoadRegistryTolerant(root).Layout(); got != "" {
		t.Errorf("after tabs: %q", got)
	}
}

func (r *layoutRig) Capabilities() host.Capabilities { return host.CapabilitiesOf(r) }

// linkedRig is herdr with work.herdrGroupSlots: each slot's pane starts in a
// workspace of its own (ws-<slot>), and herdr closes a workspace with its last
// tab, so a split leaves the slots in the orchestrator's workspace W.
type linkedRig struct{ *layoutRig }

func (r *linkedRig) Capabilities() host.Capabilities { return host.CapabilitiesOf(r) }

func (r *linkedRig) setWorkspace(pane, ws string) {
	for i := range r.panes {
		if r.panes[i].ID == pane {
			r.panes[i].Workspace = ws
		}
	}
}

func (r *linkedRig) inWorkspace(ws string) bool {
	for _, p := range r.panes {
		if p.Workspace == ws {
			return true
		}
	}
	return false
}

// land moves pane into workspace ws. Like herdr, a pane that changes workspace
// gets a new id (p-ben becomes p-ben'), and the old id is gone.
func (r *linkedRig) land(pane, ws string) (string, error) {
	for i := range r.panes {
		if r.panes[i].ID != pane {
			continue
		}
		if r.panes[i].Workspace == ws {
			return pane, nil
		}
		r.panes[i].Workspace, r.panes[i].ID = ws, pane+"'"
		for slot, p := range r.agent {
			if p == pane {
				r.agent[slot] = pane + "'"
			}
		}
		return pane + "'", nil
	}
	return "", fmt.Errorf("pane %s not found", pane)
}

func (r *linkedRig) SplitInto(ctx context.Context, pane, tab, target, dir string, keep float64) error {
	if err := r.layoutRig.SplitInto(ctx, pane, tab, target, dir, keep); err != nil {
		return err
	}
	_, err := r.land(pane, "W")
	return err
}

func (r *linkedRig) ToNewTab(ctx context.Context, pane, label string) error {
	if err := r.layoutRig.ToNewTab(ctx, pane, label); err != nil {
		return err
	}
	_, err := r.land(pane, "W")
	return err
}

func (r *linkedRig) OpenLinked(_ context.Context, primary, slot, path string) (string, string, error) {
	r.log = append(r.log, fmt.Sprintf("open %s %s %s", primary, slot, filepath.Base(path)))
	if slot == r.cannotLink {
		return "", "", fmt.Errorf("%w: unknown_method", host.ErrUnsupported)
	}
	if r.inWorkspace("ws-" + slot) {
		return "ws-" + slot, "", nil
	}
	return "ws-" + slot, "root-" + slot, nil
}

func (r *linkedRig) ToNewTabIn(_ context.Context, pane, ws, label string) error {
	r.log = append(r.log, fmt.Sprintf("tab-in %s %s %s", pane, ws, label))
	if label == r.failIn {
		return fmt.Errorf("herdr pane move %s failed", pane)
	}
	id, err := r.land(pane, ws)
	r.setTab(id, "t-"+label)
	return err
}

func (r *linkedRig) CloseWorkspace(context.Context, string) error { return nil }

func (r *linkedRig) CloseTab(_ context.Context, tab string) error {
	r.log = append(r.log, "close "+tab)
	return nil
}

func groupedProject(t *testing.T, slots ...string) (string, *linkedRig, *Deps) {
	t.Helper()
	root, rig, deps := layoutProject(t, "herdr", slots, slots...)
	if err := os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(`{"work":{"herdrGroupSlots":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range rig.panes {
		rig.panes[i].Workspace = "W"
		if s := strings.TrimPrefix(rig.panes[i].ID, "p-"); s != rig.panes[i].ID {
			rig.panes[i].Workspace = "ws-" + s
		}
	}
	for _, s := range slots {
		name := s
		worker.UpdateSlot(root, name, func(sl *worker.Slot) {
			sl.SetWorkspaceID("ws-" + name)
		})
	}
	return root, &linkedRig{rig}, func() *Deps { deps.Host = func(host.Kind) host.Host { return &linkedRig{rig} }; return deps }()
}

func workspaceIDs(root string, slots ...string) string {
	reg := worker.LoadRegistryTolerant(root)
	var out []string
	for _, s := range slots {
		out = append(out, s+"="+reg.Slot(s).WorkspaceID())
	}
	return strings.Join(out, ",")
}

// AC-1..3: split closes the linked workspaces (said in one line, ids cleared),
// tabs reopens them and puts each pane back, and a second split still finds
// every slot.
func TestLayoutGroupedSplitTabsSplit(t *testing.T) {
	root, rig, deps := groupedProject(t, "ben", "kit")

	code, env, errs := rotaRunWith(t, deps, "--json", "layout", "split", "--project", root)
	if note := jsonx.Str(layoutRows(env)[0], "note"); code != 0 || note != "slots leave their linked workspaces until `rota layout tabs`" {
		t.Fatalf("split: exit %d note %q: %s", code, note, errs)
	}
	if got := workspaceIDs(root, "ben", "kit"); got != "ben=,kit=" {
		t.Errorf("after split: %s", got)
	}
	// The report names the panes as they are now, none of them foreign.
	row := layoutRows(env)[0]
	var ids []string
	ws, _ := row.Get("workers")
	for _, w := range ws.([]any) {
		ids = append(ids, jsonx.Str(w.(*jsonx.Object), "pane"))
	}
	if got := strings.Join(ids, ","); got != "p-ben',p-kit'" {
		t.Errorf("split report panes = %s", got)
	}
	if fo, _ := row.Get("foreign"); len(fo.([]any)) != 0 || strings.Contains(errs, "not rota's") {
		t.Errorf("split reported foreign panes: %v / %s", fo, errs)
	}

	rig.log = nil
	if code, _, errs := rotaRunWith(t, deps, "--json", "layout", "tabs", "--project", root); code != 0 {
		t.Fatalf("tabs: exit %d: %s", code, errs)
	}
	// The split renumbered both panes: tabs finds them again by agent.
	want := "open W ben ben|tab-in p-ben' ws-ben ben|close root-ben|open W kit kit|tab-in p-kit' ws-kit kit|close root-kit|rename tab T orchestrator"
	if got := strings.Join(rig.log, "|"); got != want {
		t.Errorf("tabs calls:\n%s\nwant\n%s", got, want)
	}
	if got := workspaceIDs(root, "ben", "kit"); got != "ben=ws-ben,kit=ws-kit" {
		t.Errorf("after tabs: %s", got)
	}

	code, env, errs = rotaRunWith(t, deps, "--json", "layout", "split", "--project", root)
	rows := layoutRows(env)
	if code != 0 || len(rows) != 1 {
		t.Fatalf("second split: exit %d: %s", code, errs)
	}
	if ws, _ := rows[0].Get("workers"); len(ws.([]any)) != 2 {
		t.Errorf("second split lost a slot: %v", ws)
	}
	if got := workspaceIDs(root, "ben", "kit"); got != "ben=,kit=" {
		t.Errorf("after second split: %s", got)
	}
}

// AC-6: grouping off, tabs is the plain tab move and nothing is recorded.
func TestLayoutUngroupedTabsKeepsPlainTabs(t *testing.T) {
	root, rig, deps := groupedProject(t, "ben")
	os.WriteFile(filepath.Join(root, ".rota", "config.json"), []byte(`{"work":{"herdrGroupSlots":false}}`), 0o644)
	rotaRunWith(t, deps, "--json", "layout", "split", "--project", root)
	rig.log = nil
	rotaRunWith(t, deps, "--json", "layout", "tabs", "--project", root)
	if got := strings.Join(rig.log, "|"); got != "tab p-ben' ben|rename tab T orchestrator" {
		t.Errorf("calls = %s", got)
	}
	if got := workspaceIDs(root, "ben"); got != "ben=ws-ben" {
		t.Errorf("workspaceId touched: %s", got)
	}
}

// A move that fails after others succeeded still records where the slots are:
// ben is back in ws-ben, kit never left the orchestrator's workspace, and the
// root tab the open made for kit is closed.
func TestLayoutGroupedTabsFailurePartway(t *testing.T) {
	root, rig, deps := groupedProject(t, "ben", "kit")
	rotaRunWith(t, deps, "--json", "layout", "split", "--project", root)
	rig.log, rig.failIn = nil, "kit"
	code, _, errs := rotaRunWith(t, deps, "--json", "layout", "tabs", "--project", root)
	if code == 0 {
		t.Fatalf("want a failure: %s", errs)
	}
	if got := workspaceIDs(root, "ben", "kit"); got != "ben=ws-ben,kit=" {
		t.Errorf("workspaceIds = %s", got)
	}
	if got := strings.Join(rig.log, "|"); !strings.HasSuffix(got, "open W kit kit|tab-in p-kit' ws-kit kit|close root-kit") {
		t.Errorf("calls = %s", got)
	}
}

// Layout and dispatch group on the same condition: a herdr that can close
// workspaces but not link panes is not grouped, so tabs stays plain.
func TestLayoutGroupedNeedsALinkingHost(t *testing.T) {
	root, rig, deps := groupedProject(t, "ben")
	deps.Host = func(host.Kind) host.Host { return &closerOnlyRig{rig.layoutRig} }
	rotaRunWith(t, deps, "--json", "layout", "split", "--project", root)
	rig.log = nil
	rotaRunWith(t, deps, "--json", "layout", "tabs", "--project", root)
	if got := strings.Join(rig.log, "|"); got != "tab p-ben ben|rename tab T orchestrator" {
		t.Errorf("calls = %s", got)
	}
	if got := workspaceIDs(root, "ben"); got != "ben=ws-ben" {
		t.Errorf("an ungrouped round's workspaceId was rewritten: %s", got)
	}
}

// closerOnlyRig closes workspaces but cannot link panes.
type closerOnlyRig struct{ *layoutRig }

func (r *closerOnlyRig) Capabilities() host.Capabilities              { return host.CapabilitiesOf(r) }
func (r *closerOnlyRig) CloseWorkspace(context.Context, string) error { return nil }

// A herdr that cannot link: tabs warns once, keeps that slot a plain tab with
// no workspaceId, and still returns the others to their workspaces.
func TestLayoutGroupedTabsWarnsWhenHerdrCannotLink(t *testing.T) {
	root, rig, deps := groupedProject(t, "ben", "kit")
	rotaRunWith(t, deps, "--json", "layout", "split", "--project", root)
	rig.cannotLink = "kit"
	code, _, errs := rotaRunWith(t, deps, "--json", "layout", "tabs", "--project", root)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if !strings.Contains(errs, "herdr cannot link workspaces; slot kit stays a tab") || strings.Contains(errs, "slot ben") {
		t.Errorf("stderr = %q", errs)
	}
	if got := workspaceIDs(root, "ben", "kit"); got != "ben=ws-ben,kit=" {
		t.Errorf("workspaceIds = %s", got)
	}
}
