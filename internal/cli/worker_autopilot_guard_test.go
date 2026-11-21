package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/rotastatetest"
	"github.com/l4ci/rota/internal/roundlease"
)

// armedProject is a worker project with a live watch marker for pid 4242 and
// the Deps that see that pid alive.
func armedProject(t *testing.T, autopilot bool) (*Deps, string) {
	t.Helper()
	dir := workerProject(t, `{"ship":{"review":"none"},"test":{"full":["true"]}}`)
	if code, out, errOut := rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main"); code != 0 {
		t.Fatalf("init: %s %s", out, errOut)
	}
	le := roundlease.DefaultEnv()
	le.Alive = func(p int) bool { return p == 4242 }
	le.StartTime = func(p int) (uint64, bool) { return 99, p == 4242 }
	cd, err := rotastatetest.CommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roundlease.Arm(le, cd, 4242, time.Minute, autopilot); err != nil {
		t.Fatal(err)
	}
	d := depsIn(dir)
	d.LeaseEnv = func() roundlease.Env { return le }
	return d, dir
}

func TestWorkerLandingVerbsRefuseDuringAutopilot(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		autopilot bool
		refused   bool
	}{
		{"gate", []string{"worker", "gate", "w1", "--base", "main"}, true, true},
		{"train", []string{"worker", "train", "w1", "--base", "main"}, true, true},
		{"gate check-only", []string{"worker", "gate", "w1", "--base", "main", "--check-only"}, true, false},
		{"train plan", []string{"worker", "train", "w1", "--base", "main", "--plan"}, true, false},
		{"gate override", []string{"worker", "gate", "w1", "--base", "main", "--ignore-autopilot"}, true, false},
		{"train override", []string{"worker", "train", "w1", "--base", "main", "--ignore-autopilot"}, true, false},
		{"gate plain watch", []string{"worker", "gate", "w1", "--base", "main"}, false, false},
		{"train plain watch", []string{"worker", "train", "w1", "--base", "main"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, dir := armedProject(t, tc.autopilot)
			code, out, errOut := rotaInWith(t, d, dir, append([]string{"--json"}, tc.args...)...)
			got := data(t, out)["blockedBy"] == "autopilot-armed"
			if got != tc.refused {
				t.Fatalf("refused=%v want %v: %d %s %s", got, tc.refused, code, out, errOut)
			}
			if tc.refused && (code != 4 || !strings.Contains(errOut+out, "plain watch")) {
				t.Fatalf("want exit 4 with a plain-watch hint: %d %s %s", code, out, errOut)
			}
		})
	}
}
