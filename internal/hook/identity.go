package hook

import (
	"github.com/l4ci/rota/internal/roundlease"
)

// Who says how a hook process relates to the round lease.
type Who struct {
	Lease        roundlease.Lease
	State        roundlease.State
	Orchestrator bool // holds a live lease
	LeaseFree    bool // no lease, or a stale one (its holder is gone)
}

// Identify decides whether the process behind a hook is the orchestrator: its
// nearest non-shell ancestor (or holderPID when non-zero) must be the live
// lease's holder, matched the way `rota round start` recorded it. A read error
// is "not the orchestrator".
func Identify(env roundlease.Env, getenv func(string) string, holderPID int, commonDir string) Who {
	l, st, err := env.Read(commonDir)
	w := Who{Lease: l, State: st}
	if err != nil {
		return w
	}
	w.LeaseFree = st == roundlease.None || st == roundlease.Stale
	if st == roundlease.Live && env.Discover(holderPID, getenv).SameAs(l, env.Host) {
		w.Orchestrator = true
	}
	return w
}
