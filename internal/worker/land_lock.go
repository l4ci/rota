package worker

import (
	"context"
	"errors"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotastate"
)

// landLockTimeout bounds the wait for another gate or train on the same
// repository. A verify (or a CI wait) held under the lock can run for many
// minutes, so the bound is generous: it exists to surface a wedged holder.
const landLockTimeout = 4 * time.Hour

// withLandLock runs fn while holding the repository's land lock: one
// <git-common-dir>/rota/land.lock shared by every worktree of the repo. A gate
// and a train take it before their first write (the train's scratch worktree,
// the gate's merge into the base) and hold it through verify and landing, so a
// second one queues instead of changing the worktree list under the first's
// verify or landing inside the first's verify-to-land window. The lock is
// taken outside test/gate.sh's machine-wide lock, never the reverse, so the
// two cannot deadlock. A train's own landing steps run under the lock it
// already holds (GateOpts.HoldsLandLock) and do not retake it.
func (e Env) withLandLock(ctx context.Context, root string, fn func(waited bool) error) error {
	common, err := rotastate.CommonDirVia(ctx, e.Git, root)
	if err != nil {
		return err
	}
	path := rotastate.File(common, "land")
	// waited: the lock was busy, so what fn is about to judge may have changed.
	err = fsio.Locked(path, 0, func() error { return fn(false) })
	if errors.Is(err, fsio.ErrLockTimeout) {
		return fsio.Locked(path, landLockTimeout, func() error { return fn(true) })
	}
	return err
}

// LandLockHeld reports whether a gate or train holds the repository's land
// lock right now. It probes without waiting and releases the lock at once, so
// the answer is a snapshot: a verb that must not run beside a gate refuses on
// true and the caller reruns it once the gate exits.
func (e Env) LandLockHeld(ctx context.Context, root string) (bool, error) {
	common, err := rotastate.CommonDirVia(ctx, e.Git, root)
	if err != nil {
		return false, err
	}
	err = fsio.Locked(rotastate.File(common, "land"), 0, func() error { return nil })
	if errors.Is(err, fsio.ErrLockTimeout) {
		return true, nil
	}
	return false, err
}
