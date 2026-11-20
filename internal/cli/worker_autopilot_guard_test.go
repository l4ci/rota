package cli

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/roundlease"
)

// armWatch leaves a live watch marker for pid 4242 under the project.
func armWatch(t *testing.T, autopilot bool) (*Ctx, string) {
	t.Helper()
	root := orchProject(t, false)
	le := roundlease.DefaultEnv()
	le.Alive = func(p int) bool { return p == 4242 }
	le.StartTime = func(p int) (uint64, bool) { return 99, p == 4242 }
	cd, err := rotastatetest.CommonDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roundlease.Arm(le, cd, 4242, time.Minute, autopilot); err != nil {
		t.Fatal(err)
	}
	d := testDeps()
	d.LeaseEnv = func() roundlease.Env { return le }
	return &Ctx{Deps: d}, root
}

func TestRefuseDuringAutopilot(t *testing.T) {
	c, root := armWatch(t, true)
	if err := refuseDuringAutopilot(c, root, false); err == nil {
		t.Error("live autopilot watch: want a refusal")
	}
	if err := refuseDuringAutopilot(c, root, true); err != nil {
		t.Errorf("override flag: want no refusal, got %v", err)
	}
	c, root = armWatch(t, false)
	if err := refuseDuringAutopilot(c, root, false); err != nil {
		t.Errorf("plain watch: want no refusal, got %v", err)
	}
}
