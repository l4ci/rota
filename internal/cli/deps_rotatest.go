//go:build rotatest

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/doctor"
	"github.com/l4ci/rota/internal/worker"
)

// Smoke and frozen-record binaries inject fixtures at the same boundaries as
// unit tests. This wiring and its environment reads do not enter release builds
// or defaultDeps, which always describes the real machine.
func invocationDeps() *Deps {
	d := defaultDeps()
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
