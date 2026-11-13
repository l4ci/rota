package worker

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
)

// A harness switch (claude to codex) spawns a session whose input comes up
// late: the host reports it still booting. The brief waits for it (#3165).
func TestDispatchTaskWaitsForLateSessionInput(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	h := tmuxFake()
	h.statusSeq = map[string][]string{"w1": {"working", "unknown", "working", "idle"}}
	res, err := envWith(h).DispatchTask(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"})
	if err != nil {
		t.Fatalf("a session ready late still gets its brief: %v", err)
	}
	if res.Handle == "" || !strings.Contains(h.sent, "task") {
		t.Fatalf("brief not delivered: %+v sent=%q", res, h.sent)
	}
	if got := h.statusPolls; got != 4 {
		t.Errorf("polled the host %d times before sending, want 4", got)
	}
	var sendAt int
	for i, c := range h.calls {
		if strings.HasPrefix(c, "send ") {
			sendAt = i
		}
	}
	if h.statusPolls != 4 || sendAt == 0 {
		t.Errorf("the brief went out before the session was ready: %v", h.calls)
	}
}

// herdr reports a booted, idle session it cannot classify as "unknown" for
// good: the brief must not wait out the boot timeout on it.
func TestDispatchTaskSendsWhenUnknownStatusButPaneBooted(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	h := tmuxFake()
	h.status = map[string]string{"w1": "unknown"}
	h.panes = map[string][]string{"w1": {"? for shortcuts\n", "? for shortcuts\n", "? for shortcuts\n"}}
	res, err := envWith(h).DispatchTask(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1", BootTimeout: 3})
	if err != nil {
		t.Fatalf("a booted pane with unknown status still gets its brief: %v", err)
	}
	if res.Handle == "" || !strings.Contains(h.sent, "task") {
		t.Fatalf("brief not delivered: %+v sent=%q", res, h.sent)
	}
}

// A status the host cannot classify from the start (the dry-round fake herdr)
// never blocks the brief, even with a pane that shows no prompt.
func TestDispatchTaskSendsWhenStatusStaysUnknownFromStart(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	h := tmuxFake()
	h.status = map[string]string{"w1": "unknown"}
	if _, err := envWith(h).DispatchTask(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1", BootTimeout: 3}); err != nil {
		t.Fatalf("unknown from the start still gets its brief: %v", err)
	}
	if !strings.Contains(h.sent, "task") {
		t.Fatalf("brief not delivered: %q", h.sent)
	}
}

// A session still booting at the boot timeout fails the dispatch stage, with
// nothing sent, so the receipt's replay delivers it later.
func TestDispatchTaskFailsWhenSessionNeverAcceptsInput(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	h := tmuxFake()
	h.status = map[string]string{"w1": "working"}
	_, err := envWith(h).DispatchTask(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1", BootTimeout: 3})
	if err == nil || exitOf(err) != exitcode.ExitRetry {
		t.Fatalf("want a retryable dispatch failure, got %v (exit %d)", err, exitOf(err))
	}
	if !strings.Contains(err.Error(), "accept input") || !strings.Contains(err.Error(), "3s") {
		t.Errorf("the error names the wait and its bound: %v", err)
	}
	if h.sent != "" {
		t.Errorf("nothing is sent into a session that is not ready: %q", h.sent)
	}
}

// A host with no native state has nothing to wait on.
func TestDispatchTaskSendsAtOnceWithoutNativeState(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	h := tmuxFake()
	if _, err := envWith(h).DispatchTask(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if got := h.statusPolls; got > 1 {
		t.Errorf("polled %d times with no native state", got)
	}
}
