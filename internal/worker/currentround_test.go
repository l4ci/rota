package worker

import (
	"testing"

	"github.com/l4ci/rota/internal/roundlease"
)

func TestCurrentRoundAuthorityOrder(t *testing.T) {
	cases := []struct {
		name string
		src  RoundSources
		want int
	}{
		{"nothing knows", RoundSources{}, 0},
		{"ledger only", RoundSources{LedgerRound: 3}, 3},
		{"registry beats ledger", RoundSources{Registry: 5, HasRegistry: true, LedgerRound: 9}, 5},
		{"registry round 0 is a found round", RoundSources{Registry: 0, HasRegistry: true, LedgerRound: 4}, 0},
		// After a reclaim the new holder's lease is prev+1 while the registry still holds the dead round.
		{"live lease beats registry after a reclaim", RoundSources{Lease: roundlease.Lease{Round: 8}, LeaseState: roundlease.Live, Registry: 7, HasRegistry: true, LedgerRound: 7}, 8},
		{"foreign lease counts", RoundSources{Lease: roundlease.Lease{Round: 8}, LeaseState: roundlease.Foreign, Registry: 7, HasRegistry: true}, 8},
		{"stale lease is nobody's round", RoundSources{Lease: roundlease.Lease{Round: 8}, LeaseState: roundlease.Stale, Registry: 7, HasRegistry: true}, 7},
	}
	for _, c := range cases {
		if got := CurrentRound(c.src); got != c.want {
			t.Errorf("%s: round %d, want %d", c.name, got, c.want)
		}
	}
}
