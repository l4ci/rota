package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

// groupHost spawns into a linked workspace and records the workspace closes.
type groupHost struct {
	*fakeHost
	closed []string
	// cannotLink is a herdr that falls back to a tab: Spawn links nothing.
	cannotLink bool
}

func (g *groupHost) Capabilities() host.Capabilities { return host.CapabilitiesOf(g) }
func (g *groupHost) Spawn(ctx context.Context, o host.SpawnOpts) (string, error) {
	h, err := g.fakeHost.Spawn(ctx, o)
	if err == nil && o.LinkWorkspace && !g.cannotLink {
		if o.Linked != nil {
			*o.Linked = true
		}
		return "w9N:t2", nil
	}
	return h, err
}
func (g *groupHost) CloseWorkspace(_ context.Context, id string) error {
	g.closed = append(g.closed, id)
	return nil
}

// The layout side of a host that can group; dispatch only asks that it exists.
func (g *groupHost) OpenLinked(context.Context, string, string, string) (string, string, error) {
	return "", "", nil
}
func (g *groupHost) ToNewTabIn(context.Context, string, string, string) error { return nil }
func (g *groupHost) CloseTab(context.Context, string) error                   { return nil }

func groupProject(t *testing.T, cfg string, handles map[string]string) string {
	t.Helper()
	dir := waitProject(t, 1, handles)
	if cfg != "" {
		os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(cfg), 0o644)
	}
	return dir
}

// dispatchOnceWith dispatches with raw as the project config, in memory ("" is
// an empty one).
func dispatchOnceWith(t *testing.T, dir string, h host.Host, raw string) error {
	t.Helper()
	if raw == "" {
		raw = "{}"
	}
	v, err := jsonx.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	e := envWith(h)
	e.Config = func(string) any { return v }
	_, err = e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"})
	return err
}

func dispatchOnce(t *testing.T, dir string, h host.Host) error {
	t.Helper()
	_, err := envWith(h).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"})
	return err
}

// AC-2: the wiring from round.herdrGroupSlots to SpawnOpts.LinkWorkspace.
func TestDispatchLinksWorkspaceOnlyWhenTheKeyIsOn(t *testing.T) {
	for cfg, want := range map[string]bool{
		``: true, `{"round":{"herdrGroupSlots":false}}`: false, `{"round":{"herdrGroupSlots":true}}`: true,
	} {
		dir := groupProject(t, "", nil)
		os.Remove(filepath.Join(dir, ".rota", "config.json")) // the seam, not the file, carries the settings
		h := &groupHost{fakeHost: tmuxFake()}
		if err := dispatchOnceWith(t, dir, h, cfg); err != nil {
			t.Fatalf("%q: %v", cfg, err)
		}
		if h.spawnOpts.LinkWorkspace != want {
			t.Errorf("config %q: LinkWorkspace = %v, want %v", cfg, h.spawnOpts.LinkWorkspace, want)
		}
		wantWS := ""
		if want {
			wantWS = "w9N"
		}
		if got := LoadRegistryTolerant(dir).Slot("w1").WorkspaceID(); got != wantWS {
			t.Errorf("config %q: workspaceId = %q, want %q", cfg, got, wantWS)
		}
	}
}

func TestDispatchRefusesANonBooleanGroupKey(t *testing.T) {
	dir := groupProject(t, "", nil)
	h := &groupHost{fakeHost: tmuxFake()}
	err := dispatchOnceWith(t, dir, h, `{"round":{"herdrGroupSlots":"yes"}}`)
	if err == nil || !strings.Contains(err.Error(), "round.herdrGroupSlots") {
		t.Fatalf("err = %v, want it to name the key", err)
	}
	if h.spawnOpts.Slot != "" {
		t.Error("spawned despite the bad config")
	}
}

// AC-5: the next dispatch kills the old session, then closes only the
// workspace the slot recorded.
func TestDispatchClosesTheRecordedWorkspaceAfterTheKill(t *testing.T) {
	dir := groupProject(t, `{"round":{"herdrGroupSlots":true}}`, map[string]string{"w1": "w9N:t2"})
	UpdateSlot(dir, "w1", func(s *Slot) { s.SetWorkspaceID("w9N") })
	h := &groupHost{fakeHost: tmuxFake()}
	if err := dispatchOnce(t, dir, h); err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.closed, ",") != "w9N" {
		t.Errorf("closed = %v, want [w9N]", h.closed)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").WorkspaceID(); got != "w9N" {
		t.Errorf("workspaceId after redispatch = %q, want the new one", got)
	}
}

// A handle from the default path has no recorded workspace: nothing closes,
// even though its id prefix differs from the orchestrator's.
func TestDispatchClosesNoWorkspaceForADefaultPathHandle(t *testing.T) {
	dir := groupProject(t, "", map[string]string{"w1": "w4:t2"})
	h := &groupHost{fakeHost: tmuxFake()}
	if err := dispatchOnce(t, dir, h); err != nil {
		t.Fatal(err)
	}
	if len(h.closed) != 0 {
		t.Errorf("closed = %v", h.closed)
	}
}

func TestKillSlotClosesTheRecordedWorkspaceAndForgetsIt(t *testing.T) {
	dir := groupProject(t, "", map[string]string{"w1": "w9N:t2"})
	UpdateSlot(dir, "w1", func(s *Slot) { s.SetWorkspaceID("w9N") })
	h := &groupHost{fakeHost: tmuxFake()}
	if err := envWith(h).KillSlot(bg, dir, "w1"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.closed, ",") != "w9N" || LoadRegistryTolerant(dir).Slot("w1").WorkspaceID() != "" {
		t.Errorf("closed = %v, workspaceId = %q", h.closed, LoadRegistryTolerant(dir).Slot("w1").WorkspaceID())
	}
}

// A host with no linked workspaces (tmux) is never linked, so the default
// being on records no workspaceId from a handle that names no workspace.
func TestDispatchOnAHostWithoutWorkspacesLinksNothing(t *testing.T) {
	dir := groupProject(t, "", nil)
	h := tmuxFake()
	if err := dispatchOnce(t, dir, h); err != nil {
		t.Fatal(err)
	}
	if h.spawnOpts.LinkWorkspace {
		t.Error("LinkWorkspace on a host that cannot close workspaces")
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").WorkspaceID(); got != "" {
		t.Errorf("workspaceId = %q, want none", got)
	}
}

// Dispatch groups on the same capability layout does: a host that closes
// workspaces but cannot link panes keeps its slots in tabs.
type closerOnlyHost struct{ *fakeHost }

func (g *closerOnlyHost) Capabilities() host.Capabilities              { return host.CapabilitiesOf(g) }
func (g *closerOnlyHost) CloseWorkspace(context.Context, string) error { return nil }

func TestDispatchNeedsAHostThatCanLinkToGroup(t *testing.T) {
	dir := groupProject(t, `{"round":{"herdrGroupSlots":true}}`, nil)
	h := &closerOnlyHost{tmuxFake()}
	if err := dispatchOnce(t, dir, h); err != nil {
		t.Fatal(err)
	}
	if h.spawnOpts.LinkWorkspace {
		t.Error("LinkWorkspace on a host that cannot link panes")
	}
}

// A herdr that cannot link falls back to a tab at Spawn: nothing is recorded
// as a workspace, so a later kill closes none.
func TestDispatchRecordsNoWorkspaceWhenHerdrFellBackToATab(t *testing.T) {
	dir := groupProject(t, `{"round":{"herdrGroupSlots":true}}`, nil)
	h := &groupHost{fakeHost: tmuxFake(), cannotLink: true}
	if err := dispatchOnce(t, dir, h); err != nil {
		t.Fatal(err)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").WorkspaceID(); got != "" {
		t.Errorf("workspaceId = %q, want none", got)
	}
}

// memConfig is an in-memory project config for Env.Config.
func memConfig(t *testing.T, raw string) func(string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return func(string) any { return v }
}
