package round

import (
	"context"
	"fmt"

	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

// LeaseStale is the drift kind for a lease whose holder is gone. It is never
// repaired by Reconcile: `rota reap` clears it (ClearStaleLease), and
// `rota round start` reclaims it.
const LeaseStale = "lease-stale"

// Lease aliases, so reconcile and reap import one package.
type (
	Lease      = roundlease.Lease
	LeaseState = roundlease.State
)

// commonDir is the git common dir of root, through the Env's git.
func (e Env) commonDir(ctx context.Context, root string) (string, error) {
	return worker.CommonDir(ctx, e.Git, root)
}

// ReadLease is the repo's orchestrator lease and its state.
func (e Env) ReadLease(ctx context.Context, root string) (Lease, LeaseState, error) {
	cd, err := e.commonDir(ctx, root)
	if err != nil {
		return Lease{}, roundlease.None, err
	}
	return e.Lease.Read(cd)
}

// ClearStaleLease removes the lease when its holder is gone and reports what
// it removed. A live or foreign lease is left alone. It is the seam `rota reap`
// calls.
func (e Env) ClearStaleLease(ctx context.Context, root string) (Lease, bool, error) {
	cd, err := e.commonDir(ctx, root)
	if err != nil {
		return Lease{}, false, err
	}
	return e.Lease.ClearStale(cd)
}

// leaseFinding adds the lease-stale drift when the lease's holder is gone.
func (e Env) leaseFinding(ctx context.Context, root string, rep *Report) {
	l, st, err := e.ReadLease(ctx, root)
	if err != nil || st != roundlease.Stale {
		return
	}
	who := "an unreadable lease file"
	if l.PID > 0 {
		who = fmt.Sprintf("pid %d (round %d, started %s)", l.PID, l.Round, l.StartedAt)
	}
	rep.add(Finding{Kind: LeaseStale, Detail: "the round lease is held by " + who + ", which is gone; `rota round start` reclaims it, `rota reap` clears it"})
}
