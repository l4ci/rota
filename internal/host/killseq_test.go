package host

import (
	"strings"
	"testing"
	"time"
)

// The shared sequence closes before it polls, and a failed pre-close query
// refuses even when everything polls gone.
func TestRunKillOrdersCloseBeforeProofAndHonoursUnverifiedBefore(t *testing.T) {
	for _, before := range []bool{false, true} {
		var log []string
		d := &Deps{KillWait: 2, Alive: func(int) bool { return false }, Sleep: func(d time.Duration) {}}
		out, err := runKill(killSteps{
			deps: d, pids: []int{7}, unverifiedBefore: before,
			close: func() error { log = append(log, "close"); return nil },
			gone:  func() presence { log = append(log, "gone"); return presentGone },
		})
		if err != nil || strings.Join(log, ",") != "close,gone" {
			t.Fatalf("before=%v: log=%v err=%v", before, log, err)
		}
		if out.ok == before {
			t.Errorf("before=%v: ok=%v", before, out.ok)
		}
		werr := out.err("w1", "tmux", "window x")
		if before != (werr != nil && strings.Contains(werr.Error(), "could not be verified (window x): tmux query failed before the close")) {
			t.Errorf("before=%v: wording %v", before, werr)
		}
	}
}
