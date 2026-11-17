package worker

import "github.com/l4ci/rota/internal/roundlease"

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
