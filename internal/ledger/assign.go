package ledger

import "github.com/l4ci/rota/internal/tracker"

// AssignQuery selects assignments for an issue, optionally restricted to a slot
// or to entries that record a harness.
type AssignQuery struct {
	Issue          string
	Slot           string
	RequireHarness bool
	// First returns the earliest matching assignment instead of the latest.
	First bool
}

// LatestAssign returns the last matching assignment in file order (the first
// with q.First), or nil.
// An empty Slot matches any slot. Issue IDs use the tracker's normalisation.
func LatestAssign(entries []Entry, q AssignQuery) *Entry {
	issue := tracker.NormRef(q.Issue)
	if issue == "" {
		return nil
	}
	for n := range entries {
		i := len(entries) - 1 - n
		if q.First {
			i = n
		}
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
