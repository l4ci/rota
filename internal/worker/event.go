package worker

import "github.com/l4ci/rota/internal/ledger"

// Worker event kinds: what `rota worker send`, `worker done` and `worker reply`
// record on the slot.
const (
	EventBlocked     = "blocked"
	EventQuestion    = "question"
	EventDone        = "done"
	EventReviewReply = "review-reply"
	// EventHeartbeat is a send kind only: it is recorded by Heartbeat and
	// never stands as a slot's event.
	EventHeartbeat = "heartbeat"
)

// EventSpec is everything one event kind means, read by every consumer: the
// slot state it records, its ledger row, the poll row it decides and whether
// `worker send` takes it.
type EventSpec struct {
	Kind string
	// State is the slot state the event records; "" is no change (a heartbeat
	// proves the worker alive; a reply posted mid-fix does not finish the work,
	// so a busy slot stays busy).
	State string
	// Ledger is the ledger kind the event writes, "" for none. Without
	// EveryRow it is written only on the edge into State.
	Ledger string
	// EveryRow writes the ledger row for every event, not only on a state edge.
	EveryRow bool
	// RowBody puts the event kind and body in the ledger row's detail.
	RowBody bool
	// Poll is the poll state a live event decides over a static pane (see
	// Decide), "" when it never decides.
	Poll string
	// Send is set when `worker send` takes the kind; SendBody when its text is required.
	Send, SendBody bool
	// Help is the `worker send` flag's usage text.
	Help string
}

// Events is the one table of event kinds, in declaration order.
var Events = []EventSpec{
	{Kind: EventBlocked, State: "blocked", Ledger: ledger.KindBlocked, EveryRow: true, RowBody: true, Poll: StateBlocked, Send: true, SendBody: true, Help: "the one question the worker is blocked on"},
	{Kind: EventQuestion, State: "blocked", Ledger: ledger.KindBlocked, EveryRow: true, RowBody: true, Poll: StateBlocked, Send: true, SendBody: true, Help: "a question for the orchestrator; the worker waits for the answer"},
	{Kind: EventDone, State: "done", Ledger: ledger.KindDone, Poll: StateDone},
	{Kind: EventReviewReply, Ledger: ledger.KindReviewReply, EveryRow: true, Poll: StateDone},
	{Kind: EventHeartbeat, Send: true, Help: "alive, not progress: restarts the stall clock, writes no ledger row"},
}

// EventKinds are the kinds SetEvent accepts.
var EventKinds = func() []string {
	kinds := make([]string, len(Events))
	for i, e := range Events {
		kinds[i] = e.Kind
	}
	return kinds
}()

// EventSpecFor returns the table row of a kind.
func EventSpecFor(kind string) (EventSpec, bool) {
	for _, e := range Events {
		if e.Kind == kind {
			return e, true
		}
	}
	return EventSpec{}, false
}
