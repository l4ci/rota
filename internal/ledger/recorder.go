package ledger

import (
	"cmp"
	"fmt"
	"time"
)

// Stamp is the slot facts a Recorder fills into an entry that left them empty.
type Stamp struct{ Slot, Harness, Account string }

// Recorder is the one place an entry gets its round, clock and slot facts
// before Append. Its dependencies are injected: the worker package supplies
// the lease, registry and clock, a test supplies fakes.
type Recorder struct {
	// Round is the round of the lease held at root, 0 for none.
	Round func(root string) int
	// Resolve is the slot facts of e (the slot it names, else the one holding
	// e.Issue); ok is false when none is known. A nil Resolve fills nothing.
	// It must not fail: a corrupt registry resolves to no slot, never to a
	// lost entry.
	Resolve func(root string, e Entry) (s Stamp, ok bool)
	// Now stamps an entry that carries no TS; Append refuses a zero one.
	Now func() time.Time
	// Warn receives the message of a failed Note.
	Warn func(msg string)
}

// Note appends e and warns, never fails, when the ledger cannot be written.
func (r *Recorder) Note(root string, e Entry) {
	if err := r.AppendErr(root, e); err != nil && r.Warn != nil {
		r.Warn(fmt.Sprintf("rota: round ledger not written (%s): %v", Path(root), err))
	}
}

// AppendErr is Note returning the failure, for a verb that must exit non-zero
// when its row is not written. It stamps the round unless e names one, TS
// unless e carries one, and slot, harness and account where e left them empty.
func (r *Recorder) AppendErr(root string, e Entry) error {
	if e.Round == 0 && r.Round != nil {
		e.Round = r.Round(root)
	}
	if e.TS.IsZero() && r.Now != nil {
		e.TS = r.Now()
	}
	if r.Resolve != nil && (e.Account == "" || e.Harness == "" || e.Slot == "") {
		if s, ok := r.Resolve(root, e); ok {
			e.Slot = cmp.Or(e.Slot, s.Slot)
			e.Harness = cmp.Or(e.Harness, s.Harness)
			e.Account = cmp.Or(e.Account, s.Account)
		}
	}
	return Append(root, e)
}
