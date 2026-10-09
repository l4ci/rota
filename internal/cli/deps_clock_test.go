package cli

import (
	"context"
	"path/filepath"
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

// The round env's one Now and Getenv drive the account code itself: Getenv
// points the usage dir at a fixture, and the clock decides whether its reset
// window has lapsed.
func TestRoundEnvClockAndGetenvDriveAccountMeters(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".rota", "config.json"),
		`{"work":{"accounts":[{"name":"a","configDir":"/nowhere"}]}}`)
	usage := t.TempDir()
	write(t, filepath.Join(usage, "a.json"),
		`{"five_hour":{"utilization":100,"resets_at":"2026-10-09T09:00:00+00:00"},"seven_day":{"utilization":5,"resets_at":null}}`)
	verdict := func(now time.Time) string {
		d := defaultDeps()
		d.Now = func() time.Time { return now }
		d.Getenv = func(k string) string {
			if k == "ROTA_ACCOUNT_USAGE_DIR" {
				return usage
			}
			return ""
		}
		renv := d.RoundEnvLocal(context.Background(), root)
		renv.Worker.Accounts = d.WorkerAccounts()
		ms := renv.Worker.Accounts.Meters(context.Background(), root)
		if len(ms) != 1 {
			t.Fatalf("meters = %+v", ms)
		}
		return ms[0].Verdict
	}
	before := verdict(time.Date(2026, 10, 9, 8, 0, 0, 0, time.UTC))
	after := verdict(time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC))
	if before == after || before != "cooling" {
		t.Errorf("verdict before reset %q, after %q: want cooling then free", before, after)
	}
}
