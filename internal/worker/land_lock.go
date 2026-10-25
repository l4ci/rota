package worker

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotastate"
)

// LandHolderSuffix is appended to the land lock path for the file naming its holder.
const LandHolderSuffix = ".holder"

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
// two cannot deadlock. holder names what the lock is held for (a PR, a slot or
// a train's targets), readable through LandLockHolder while it is held. A train's own landing steps run under the lock it
// already holds (GateOpts.HoldsLandLock) and do not retake it.
func (e Env) withLandLock(ctx context.Context, root, holder string, fn func(waited bool) error) error {
	common, err := rotastate.CommonDirVia(ctx, e.Git, root)
	if err != nil {
		return err
	}
	path := rotastate.File(common, "land")
	// held records who holds the lock for LandLockHolder; the lock is the
	// truth, so a stale holder file only matters while the lock is held.
	held := func(waited bool) func() error {
		return func() error {
			// best effort: the holder only sharpens a refusal. WriteFile truncates;
			// a failed write drops the file so a previous holder's name never lingers.
			if os.WriteFile(path+LandHolderSuffix, []byte(holder), 0o644) != nil {
				os.Remove(path + LandHolderSuffix)
			}
			defer os.Remove(path + LandHolderSuffix)
			return fn(waited)
		}
	}
	// waited: the lock was busy, so what fn is about to judge may have changed.
	err = fsio.Locked(path, 0, held(false))
	if errors.Is(err, fsio.ErrLockTimeout) {
		return fsio.Locked(path, landLockTimeout, held(true))
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

// LandLockHolder names what holds the land lock: the PR or slot a gate was
// given, or the train's targets. "" when nothing recorded a holder (no lock, or
// a holder from an older rota).
func (e Env) LandLockHolder(ctx context.Context, root string) string {
	common, err := rotastate.CommonDirVia(ctx, e.Git, root)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(rotastate.File(common, "land") + LandHolderSuffix)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
