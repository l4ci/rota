package hook

import (
	"fmt"
	"time"

	"github.com/l4ci/rota/internal/stamp"
)

// Context guard modes of work.contextGuard.
const (
	GuardWarn = "warn"
	GuardAuto = "auto"
)

// guardReading is the session's context percentage when its statusline
// reading is fresh; ok is false for a stale or missing one.
func guardReading(st State, set Settings, now time.Time) (pct float64, ok bool) {
	at, err := stamp.Parse(st.UpdatedAt)
	if err != nil || now.Sub(at) > time.Duration(set.StateMaxAge)*time.Second || st.ContextPct == nil {
		return 0, false
	}
	return *st.ContextPct, true
}

// GuardPromptDecision is what the prompt hook does for a non-orchestrator
// session. An empty Line prints nothing; Persist says whether State changed.
type GuardPromptDecision struct {
	Line    string
	State   State
	Persist bool
}

// DecideGuardPrompt is the warn rule: one line when a fresh reading is at or
// above the threshold and this crossing has not warned yet; the flag clears
// once a fresh reading is below it. It never blocks.
func DecideGuardPrompt(st State, set Settings, now time.Time) GuardPromptDecision {
	d := GuardPromptDecision{State: st}
	pct, ok := guardReading(st, set, now)
	if !ok {
		return d
	}
	if pct < float64(set.Threshold) {
		if st.GuardWarned {
			d.State.GuardWarned, d.Persist = false, true
		}
		return d
	}
	if st.GuardWarned {
		return d
	}
	d.State.GuardWarned, d.Persist = true, true
	d.Line = fmt.Sprintf("Context is at %s%%, at or above the handoff threshold of %d%%. "+
		"Run /rota-pause at the next task boundary so planning state survives compaction.",
		trimPct(pct), set.Threshold)
	return d
}

// mergeGuardWarned is DecideGuardPrompt's merge: only the warned flag.
func mergeGuardWarned(cur, decided State) State {
	cur.GuardWarned = decided.GuardWarned
	return cur
}

// DecideGuardStop is the auto rule: block a fresh reading at or above the
// threshold with a request for /rota-pause, at most set.HandoffMaxBlks times
// per crossing, counting every block so a re-entered stop (stop_hook_active)
// is held no more than a first one. A reading below the threshold starts a new
// crossing. A fresh handoff note written since the last block means the session
// obeyed, so the stop passes. Past the cap the stop passes, so a session that
// cannot pause is never held.
func DecideGuardStop(st State, set Settings, ho Handoff, now time.Time) StopDecision {
	d := StopDecision{State: st}
	pct, ok := guardReading(st, set, now)
	if !ok {
		return d
	}
	if pct < float64(set.Threshold) {
		if st.HandoffBlocks != 0 || st.HandoffFailed || st.BlockedAt != "" {
			d.State.HandoffBlocks, d.State.HandoffFailed, d.State.BlockedAt = 0, false, ""
			d.Persist = true
		}
		return d
	}
	if st.HandoffFailed {
		return d
	}
	if !holdForHandoff(&d, set, ho, now, handoffRule{countAll: true}) {
		return d
	}
	d.Reason = fmt.Sprintf("Context is at %s%%, at or above the handoff threshold of %d%%. "+
		"Finish the current task step, then run /rota-pause at the next task boundary before you stop.",
		trimPct(pct), set.Threshold)
	return d
}
