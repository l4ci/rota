package worker

import (
	"context"
	"github.com/l4ci/rota/internal/host"
	"testing"
)

// movedHost reports the tab a slot's pane was moved to.
type movedHost struct {
	*fakeHost
	tab string
}

func (h *movedHost) CurrentHandle(context.Context, string, string) string    { return h.tab }
func (h *movedHost) FollowTab(context.Context, string, string, string) error { return nil }

func TestRelayReportsTheTabItWasDeliveredTo(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	h := &movedHost{tmuxFake(), ""}
	e := envWith(h)
	task, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"})
	if err != nil {
		t.Fatal(err)
	}
	recorded := task.Handle

	h.tab = "w9:t79"
	res, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "answer\n"), Relay: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Handle != "w9:t79" {
		t.Errorf("relay handle = %q, want the pane's current tab w9:t79", res.Handle)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").Handle(); got != recorded {
		t.Errorf("registry handle = %q, want %q: only reconcile moves it", got, recorded)
	}

	h.tab = ""
	if res, _ = e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "again\n"), Relay: true}); res.Handle != recorded {
		t.Errorf("relay handle = %q, want the recorded %q when the host cannot say", res.Handle, recorded)
	}
}

func (h *movedHost) Capabilities() host.Capabilities { return host.CapabilitiesOf(h) }
