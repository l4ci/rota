package cli

import (
	"context"
	"testing"
	"time"
)

// One Deps clock and getenv reach the round env, the worker env and the
// accounts they hold, with no second source to keep in step.
func TestDepsClockAndGetenvReachWorkerAndAccounts(t *testing.T) {
	d := defaultDeps()
	pinned := time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC)
	d.Now = func() time.Time { return pinned }
	d.Getenv = func(k string) string {
		if k == "ROTA_ACCOUNT_USAGE_DIR" {
			return "/swapped"
		}
		return ""
	}
	acc := d.WorkerAccounts()
	if got := acc.Env.Clock()(); !got.Equal(pinned) {
		t.Errorf("accounts clock = %v, want %v", got, pinned)
	}
	if got := acc.Env.Environ()("ROTA_ACCOUNT_USAGE_DIR"); got != "/swapped" {
		t.Errorf("accounts getenv = %q, want /swapped", got)
	}
	w := d.workerEnv()
	if !w.Now().Equal(pinned) || w.Getenv("ROTA_ACCOUNT_USAGE_DIR") != "/swapped" {
		t.Errorf("worker env does not follow Deps")
	}
	r := d.RoundEnvLocal(context.Background(), t.TempDir())
	if !r.Worker.Now().Equal(pinned) || r.Worker.Getenv("ROTA_ACCOUNT_USAGE_DIR") != "/swapped" {
		t.Errorf("round env does not follow Deps")
	}
}
