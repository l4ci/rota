package worker

import (
	"context"
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
func (e Env) withLandLock(ctx context.Context, root string, fn func() error) error {
	common, err := rotastate.CommonDirVia(ctx, e.Git, root)
	if err != nil {
		return err
	}
	return fsio.Locked(rotastate.File(common, "land"), landLockTimeout, fn)
}
