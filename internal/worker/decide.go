package worker

import "time"

// Decide merges a worker's recorded event with the row its pane read as. The
// event is the worker's own word, like a sentinel, so it outranks a static
// pane that says nothing (IDLE, UNKNOWN): that is the pane `worker done` and
// `worker send --blocked` leave behind when the harness prints no sentinel.
// Everything the pane can positively show still wins: movement after the event
// (BUSY: the relay was answered, the work resumed), a sentinel (the pane's
// later word), a dead or limited session, a permission prompt.
//
// An event is live only while it is not older than the slot's last recorded
// activity (lastChange is activeAt: a pane read that moved the slot on, or a
// dispatch, which stamps it too, so an event from before the brief is dead and
// cannot classify the new session, #707). A heartbeat proves the
// worker alive and says nothing of state, so it never decides.
//
// It is pure: the pane row and the registry stamps are passed in.
func Decide(ev *Event, row PollRow, lastChange time.Time) PollRow {
	out, _ := decide(ev, row, lastChange)
	return out
}

// decide is Decide that also says whether the event decided the row.
func decide(ev *Event, row PollRow, lastChange time.Time) (PollRow, bool) {
	if ev == nil || ev.Kind == EventHeartbeat {
		return row, false
	}
	if row.State != StateIdle && row.State != StateUnknown {
		return row, false
	}
	ts := parseStamp(ev.TS)
	if ts.IsZero() || ts.Before(lastChange) {
		return row, false
	}
	switch ev.Kind {
	case EventBlocked, EventQuestion:
		return PollRow{row.Name, StateBlocked, ev.Body}, true
	case EventDone, EventReviewReply:
		evidence := ev.Body
		if evidence == "" {
			evidence = "worker " + ev.Kind + " (recorded event)"
		}
		return PollRow{row.Name, StateDone, evidence}, true
	}
	return row, false
}
