//go:build rotatest

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/update"
	"github.com/l4ci/rota/internal/worker"
)

// Smoke and frozen-record binaries inject fixtures at the same boundaries as
// unit tests. This wiring and its environment reads do not enter release builds
// or defaultDeps, which always describes the real machine.
func invocationDeps() *Deps {
	d := defaultDeps()
	d.Now, d.Today, d.HolderPID, d.ClockErr = envClock(d.Getenv)
	// ROTA_TEST_LATEST_VERSION (no network) stands in for the release read; a
	// smoke test cannot reach the update package any other way.
	if latest := d.Getenv("ROTA_TEST_LATEST_VERSION"); latest != "" {
		base := d.UpdateEnv
		d.UpdateEnv = func() update.Env {
			e := base()
			e.Latest = func() string { return latest }
			return e
		}
	}
	if path := d.Getenv("ROTA_TEST_DOCTOR_PATH"); path != "" {
		look := d.LookPath
		d.LookPath = func(name string) (string, error) {
			if strings.ContainsRune(name, os.PathSeparator) {
				return look(name)
			}
			for _, dir := range filepath.SplitList(path) {
				if dir != "" {
					if p, err := look(filepath.Join(dir, name)); err == nil {
						return p, nil
					}
				}
			}
			return "", os.ErrNotExist
		}
	}
	if disk := d.Getenv("ROTA_TEST_DOCTOR_DISK"); disk != "" {
		d.DiskUsage = func(dir string) *doctor.Disk {
			var free, total uint64
			if _, err := fmt.Sscanf(disk, "%d:%d", &free, &total); err != nil {
				return nil
			}
			return &doctor.Disk{Path: dir, Free: free, Total: total}
		}
	}
	if path := d.Getenv("ROTA_TEST_POLL_FIXTURE"); path != "" {
		kind, status := d.Getenv("ROTA_TEST_POLL_KIND"), d.Getenv("ROTA_TEST_POLL_STATUS")
		d.WorkerPoll = func(_ context.Context, _ string, opts worker.PollOpts) (worker.PollResult, error) {
			b, err := os.ReadFile(path)
			if err != nil {
				return worker.PollResult{}, Usage("fixture not found: %s", path)
			}
			slot := opts.Slot
			if slot == "" {
				slot = "fixture"
			}
			lines := opts.Lines
			if lines <= 0 {
				lines = 60
			}
			state, evidence := worker.ClassifyFor(kind, string(b), false, lines, status)
			return worker.PollResult{Slots: []worker.PollRow{{Name: slot, State: state, Evidence: evidence}}}, nil
		}
	}
	return d
}

// envClock reads the test overrides of the clock once, at the edge:
// ROTA_TEST_NOW (RFC 3339) fixes Now, ROTA_TEST_TODAY (YYYY-MM-DD) fixes the
// day alone and wins over Now there, ROTA_TEST_HOLDER_PID stands in for the
// nearest non-shell ancestor, which a smoke test cannot arrange. Not part of
// the CLI.
func envClock(getenv func(string) string) (now, today func() time.Time, holder func() int, err error) {
	now = time.Now
	if v := getenv("ROTA_TEST_NOW"); v != "" {
		if t, perr := time.Parse(time.RFC3339, v); perr == nil {
			now = func() time.Time { return t }
		}
	}
	today = now
	if v := getenv("ROTA_TEST_TODAY"); v != "" {
		if t, ok := pystr.ParseDate(v); ok {
			today = func() time.Time { return t }
		} else {
			err = Usage("ROTA_TEST_TODAY must be YYYY-MM-DD, got %q", v)
		}
	}
	pid, _ := strconv.Atoi(getenv("ROTA_TEST_HOLDER_PID"))
	return now, today, func() int { return pid }, err
}
