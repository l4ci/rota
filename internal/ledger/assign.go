package ledger

import "github.com/l4ci/rota/internal/tracker"

// AssignQuery selects assignments for an issue, optionally restricted to a slot
// or to entries that record a harness.
type AssignQuery struct {
	Issue          string
	Slot           string
	RequireHarness bool
}

// LatestAssign returns the last matching assignment in file order, or nil.
// An empty Slot matches any slot. Issue IDs use the tracker's normalisation.
func LatestAssign(entries []Entry, q AssignQuery) *Entry {
	issue := tracker.NormRef(q.Issue)
	if issue == "" {
		return nil
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := &entries[i]
		if e.Kind != KindAssign || tracker.NormRef(e.Issue) != issue {
			continue
		}
		if q.Slot != "" && e.Slot != q.Slot || q.RequireHarness && e.Harness == "" {
			continue
		}
		return e
	}
	return nil
}
