package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
)

// groupHost spawns into a linked workspace and records the workspace closes.
type groupHost struct {
	*fakeHost
	closed []string
}

func (g *groupHost) Capabilities() host.Capabilities { return host.CapabilitiesOf(g) }
func (g *groupHost) Spawn(ctx context.Context, o host.SpawnOpts) (string, error) {
	h, err := g.fakeHost.Spawn(ctx, o)
	if err == nil && o.LinkWorkspace {
		return "w9N:t2", nil
	}
	return h, err
}
func (g *groupHost) CloseWorkspace(_ context.Context, id string) error {
	g.closed = append(g.closed, id)
	return nil
}

func groupProject(t *testing.T, cfg string, handles map[string]string) string {
	t.Helper()
	dir := waitProject(t, 1, handles)
	if cfg != "" {
		os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(cfg), 0o644)
	}
	return dir
}

func dispatchOnce(t *testing.T, dir string, h host.Host) error {
	t.Helper()
	_, err := envWith(h).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"})
	return err
}

// AC-2: the wiring from work.herdrGroupSlots to SpawnOpts.LinkWorkspace.
func TestDispatchLinksWorkspaceOnlyWhenTheKeyIsOn(t *testing.T) {
	for cfg, want := range map[string]bool{
		``: false, `{"work":{"herdrGroupSlots":false}}`: false, `{"work":{"herdrGroupSlots":true}}`: true,
	} {
		dir := groupProject(t, cfg, nil)
		h := &groupHost{fakeHost: tmuxFake()}
		if err := dispatchOnce(t, dir, h); err != nil {
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
	dir := groupProject(t, `{"work":{"herdrGroupSlots":"yes"}}`, nil)
	h := &groupHost{fakeHost: tmuxFake()}
	err := dispatchOnce(t, dir, h)
	if err == nil || !strings.Contains(err.Error(), "work.herdrGroupSlots") {
		t.Fatalf("err = %v, want it to name the key", err)
	}
	if h.spawnOpts.Slot != "" {
		t.Error("spawned despite the bad config")
	}
}

// AC-5: the next dispatch kills the old session, then closes only the
// workspace the slot recorded.
func TestDispatchClosesTheRecordedWorkspaceAfterTheKill(t *testing.T) {
	dir := groupProject(t, `{"work":{"herdrGroupSlots":true}}`, map[string]string{"w1": "w9N:t2"})
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
