package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
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
	if got := acc.Env.WithDefaults().Now(); !got.Equal(pinned) {
		t.Errorf("accounts clock = %v, want %v", got, pinned)
	}
	if got := acc.Env.WithDefaults().Getenv("ROTA_ACCOUNT_USAGE_DIR"); got != "/swapped" {
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
		`{"round":{"accounts":[{"name":"a","configDir":"/nowhere"}]}}`)
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

// Deps' home and config seams reach the accounts: with no real HOME and no
// config on disk, the accounts still resolve a ~/ configDir and list the
// accounts the injected config names.
func TestDepsHomeAndConfigReachWorkerAccounts(t *testing.T) {
	t.Setenv("HOME", "")
	root := t.TempDir()
	d := defaultDeps()
	d.UserHomeDir = func() (string, error) { return "/fake/home", nil }
	d.Config = func(string) any {
		cfg, err := jsonx.Decode([]byte(`{"round":{"accounts":[{"name":"a","configDir":"~/.claude-a"}]}}`))
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	acc := d.WorkerAccounts()
	if h, _ := acc.Env.WithDefaults().Home(); h != "/fake/home" {
		t.Errorf("accounts home = %q, want /fake/home", h)
	}
	if got := acc.Configured(root); len(got) != 1 {
		t.Errorf("accounts configured = %v, want the one injected account", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".rota", "config.json")); err == nil {
		t.Fatal("test root must have no on-disk config")
	}
}
