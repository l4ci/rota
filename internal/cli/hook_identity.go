package cli

import (
	"github.com/l4ci/rota/internal/roundlease"
)

// hookWho says how a hook process relates to the round lease.
type hookWho struct {
	Lease        roundlease.Lease
	State        roundlease.State
	Orchestrator bool // holds a live lease
	LeaseFree    bool // no lease, or a stale one (its holder is gone)
}

// hookIdentify decides whether the process behind a hook is the orchestrator: its
// nearest non-shell ancestor (or holderPID when non-zero) must be the live
// lease's holder, matched the way `rota round start` recorded it. A read error
// is "not the orchestrator".
func hookIdentify(env roundlease.Env, holderPID int, getenv func(string) string, commonDir string) hookWho {
	l, st, held, err := env.Holds(commonDir, holderPID, getenv)
	w := hookWho{Lease: l, State: st}
	if err != nil {
		return w
	}
	w.LeaseFree = st == roundlease.None || st == roundlease.Stale
	w.Orchestrator = held
	return w
}
