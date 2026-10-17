package worker

import "time"

// Decide merges a worker's recorded event with the row its pane read as. The
// event is the worker's own word and it is newer than anything a static pane
// still shows, so it outranks that pane whatever sentinel is in its
// scrollback (an old ROTA-DONE, then a relay, then a `--blocked`). Only what the
// pane shows as happening now wins: movement after the event (BUSY: the relay
// was answered, the work resumed), a dead or limited session, a permission
// prompt. A done event leaves a DONE sentinel row alone: it names the PR.
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
	if ev == nil {
		return row, false
	}
	spec, _ := EventSpecFor(ev.Kind)
	if spec.Poll == "" { // a heartbeat, or a kind the table does not know
		return row, false
	}
	switch row.State {
	case StateBusy, StateDead, StateLimited, StateNeedsPermission:
		return row, false
	}
	ts := parseStamp(ev.TS)
	if ts.IsZero() || ts.Before(lastChange) {
		return row, false
	}
	switch spec.Poll {
	case StateBlocked:
		// Whatever sentinel the pane shows is older than the event: a relay
		// and a rest left it in scrollback.
		return PollRow{row.Name, StateBlocked, ev.Body}, true
	case StateDone:
		if row.State == StateDone {
			return row, false // the sentinel says the same, with the PR
		}
		evidence := ev.Body
		if evidence == "" {
			evidence = noPREvidence
		}
		return PollRow{row.Name, StateDone, evidence}, true
	}
	return row, false
}

// noPREvidence is the evidence of a done event that knew no PR: `worker done`
// runs before the PR is opened.
const noPREvidence = "worker done (recorded event), no PR recorded yet"
