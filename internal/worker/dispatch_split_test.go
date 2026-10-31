package worker

import (
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
)

// DispatchTask is a task dispatch whatever o.Relay says: the entry point, not
// a flag, picks the pipeline.
func TestDispatchTaskIgnoresRelayFlag(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	res, err := envWith(tmuxFake()).DispatchTask(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1", Relay: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Relay || res.Kind == "" {
		t.Errorf("result = %+v, want a task result with a harness kind", res)
	}
}

// DispatchRelay needs a running session and nothing else of the task pipeline.
func TestDispatchRelayNeedsSession(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	_, err := envWith(tmuxFake()).DispatchRelay(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "use 1\n")})
	if exitOf(err) != exitcode.ExitResolution {
		t.Errorf("exit = %d (%v), want %d", exitOf(err), err, exitcode.ExitResolution)
	}
}
