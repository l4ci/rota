package worker

import (
	"time"

	"github.com/l4ci/rota/internal/stamp"
)

// doneAfter is the one rule for "the slot's standing event is a `worker done`
// sent after a relay or delivery went out". The marker records the slot's
// event id (markSeq, -1 when it predates the field) and its stamp (markAt).
// The seq decides when the marker has one: stamps have second precision, so a
// stale done can share the marker's second. A marker without a seq falls back
// to the stamp, strictly later: a same-second done stays unproven rather than
// passing a stale one.
func doneAfter(ev *Event, markSeq int, markAt time.Time) bool {
	if ev == nil || ev.Kind != EventDone {
		return false
	}
	if markSeq >= 0 {
		return ev.Seq > markSeq
	}
	ts, err := stamp.Parse(ev.TS)
	return err == nil && ts.After(markAt)
}
