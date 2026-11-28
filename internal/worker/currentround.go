package worker

import (
	"context"

	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
)

// RoundSources is everything that can say which round it is. A caller fills
// the sources it may trust and leaves the rest zero.
type RoundSources struct {
	Lease       roundlease.Lease
	LeaseState  roundlease.State // None when the lease is absent or was not read
	Registry    int              // the registry's recorded round, meaningful only when HasRegistry
	HasRegistry bool             // the registry recorded a round (Registry.Round's found flag)
	LedgerRound int              // highest round the ledger names, 0 for none
}

// CurrentRound is the one answer to "which round is it". Authority order:
//
//  1. the lease, while Live or Foreign (a stale lease is nobody's round). The
//     orchestrator that holds the round writes it, so after a reclaim it is
//     newer than a registry the dead holder left;
//  2. the registry's recorded round, when it has one;
//  3. the ledger's highest round.
//
// 0 means no source knows. A caller that must not trust a source leaves it
// out: ledger stamping passes the lease alone, because a registry round that
// outlived its lease would stamp new rows with a dead round's number.
func CurrentRound(s RoundSources) int {
	if s.LeaseState == roundlease.Live || s.LeaseState == roundlease.Foreign {
		return s.Lease.Round
	}
	if s.HasRegistry {
		return s.Registry
	}
	return s.LedgerRound
}

// SigningRound is the round a relay, brief, mail, relay marker or workspace
// label carries. override, when positive, wins (--round, or a round verb's own
// reading). Otherwise it is CurrentRound over the lease and the registry, as
// the round verbs read it, so signed text and ledger rows name one round. A
// lease not yet numbered (keepalive takes it at round 0) is left out, and 1
// stands in where nothing names a round: nothing is signed with round 0.
func (e Env) SigningRound(ctx context.Context, root string, override int) int {
	if override > 0 {
		return override
	}
	e = e.WithDefaults()
	var src RoundSources
	src.Registry, src.HasRegistry = LoadRegistryTolerant(root).Round()
	// A lease that cannot be read is left out: a relay must still go out.
	if cd, err := rotastate.CommonDirVia(ctx, e.Git, root); err == nil {
		if l, st, err := e.Lease().Read(cd); err == nil && l.Round > 0 {
			src.Lease, src.LeaseState = l, st
		}
	}
	return max(CurrentRound(src), 1)
}
