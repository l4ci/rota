package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotastate"
)

var errLandingBusy = errors.New("a gate or train is running or queued; retry after it finishes")

// withLandingRun registers intent before queuing on the land lock. Manual
// runs still queue; an autopilot run (noWait) yields to every pending run.
// Admission shares a short lock so a manual registration cannot slip between
// the autopilot's idle check and its registration. Unique markers also cover
// concurrent callers in one process; PID/start-time checks recover crashes.
func (e Env) withLandingRun(ctx context.Context, root, holder string, noWait bool, fn func(bool) error) error {
	common, err := rotastate.CommonDirVia(ctx, e.Git, root)
	if err != nil {
		return err
	}
	var path string
	err = fsio.Locked(rotastate.File(common, "landing-runs"), fsio.LockTimeout, func() error {
		entries, err := os.ReadDir(rotastate.Dir(common))
		if err != nil {
			return err
		}
		for _, ent := range entries {
			if !strings.HasPrefix(ent.Name(), "landing-run-") || !strings.HasSuffix(ent.Name(), ".pid") {
				continue
			}
			p := filepath.Join(rotastate.Dir(common), ent.Name())
			b, err := os.ReadFile(p)
			if errors.Is(err, os.ErrNotExist) { // its owner just finished
				continue
			}
			if err != nil {
				return err
			}
			pid, start := parseGateMarker(string(b))
			if pid != 0 && markerLive(pid, start) {
				if noWait {
					return errLandingBusy
				}
			} else if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
		f, err := os.CreateTemp(rotastate.Dir(common), "landing-run-*.pid")
		if err != nil {
			return err
		}
		path = f.Name()
		_, err = f.WriteString(gateMarker(os.Getpid()))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	})
	if path != "" {
		defer os.Remove(path)
	}
	if err != nil {
		return err
	}
	return e.withLandLockMode(ctx, root, holder, noWait, fn)
}
