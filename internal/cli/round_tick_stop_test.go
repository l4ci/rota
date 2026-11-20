package cli

import (
	"testing"

	"github.com/l4ci/rota/internal/round"
)

// fakeStop is an in-memory round.AutopilotStop whose value can change after
// the world is built, as a wind-down does after tick entry.
type fakeStop struct {
	stopped bool
	reason  string
}

func (f *fakeStop) Load(string, int) (bool, string) { return f.stopped, f.reason }
func (f *fakeStop) Set(_ string, _ int, s bool, r string) error {
	f.stopped, f.reason = s, r
	return nil
}
func (f *fakeStop) Clear(string) error { return nil }

// tickWorld.Stop reads the stop value through the env's stop port, afresh on
// every call: a stop set after tick entry is still seen.
func TestTickWorldStopReadsThroughPortAfterEntry(t *testing.T) {
	port := &fakeStop{}
	w := &tickWorld{renv: round.Env{Stop: port}, commonDir: "cd", round: 7}
	if s, _ := w.Stop(); s {
		t.Fatal("Stop() = true before any stop was set")
	}
	_ = port.Set("cd", 7, true, round.StopVerifyFailed)
	s, reason := w.Stop()
	if !s || reason != round.StopVerifyFailed {
		t.Fatalf("Stop() = %v, %q after a stop set post-entry; want true, %q", s, reason, round.StopVerifyFailed)
	}
}
