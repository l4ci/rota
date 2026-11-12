package hook

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/l4ci/rota/internal/keepstate"
	"github.com/l4ci/rota/internal/stamp"
)

// Window names of rateLimits.
const (
	WindowFiveHour = "five_hour"
	WindowSevenDay = "seven_day"
)

// UsageOf reads the fuller of the two rate-limit windows of a session file's
// `rateLimits` (`used_percentage`, `resets_at` in epoch seconds). ok is false
// when there is no reading, as with API billing. Ties go to the 5-hour window.
func UsageOf(raw json.RawMessage) (window string, pct float64, resetsAt string, ok bool) {
	if len(raw) == 0 {
		return
	}
	type win struct {
		Used   *float64 `json:"used_percentage"`
		Resets *float64 `json:"resets_at"`
	}
	var rl struct {
		Five  *win `json:"five_hour"`
		Seven *win `json:"seven_day"`
	}
	if json.Unmarshal(raw, &rl) != nil {
		return
	}
	for _, c := range []struct {
		name string
		w    *win
	}{{WindowFiveHour, rl.Five}, {WindowSevenDay, rl.Seven}} {
		if c.w == nil || c.w.Used == nil || (ok && *c.w.Used <= pct) {
			continue
		}
		window, pct, ok, resetsAt = c.name, *c.w.Used, true, ""
		if c.w.Resets != nil {
			resetsAt = stamp.Format(time.Unix(int64(*c.w.Resets), 0))
		}
	}
	return
}

func usageReason(m keepstate.UsageHandoff, threshold int, path string) string {
	reset := ""
	if m.ResetsAt != "" {
		reset = ", resets " + m.ResetsAt
	}
	return fmt.Sprintf("Usage is at %s%% of the %s window%s, at or above the switch threshold of %d%%. "+
		"The next orchestrator session may run under another account. ",
		trimPct(m.UsedPct), m.Window, reset, threshold) + handoffInstruction("it", path)
}

// UsageMarker finds the newest usage handoff written at or after since in the
// session files whose cwd is the project root (D4).
func UsageMarker(commonDir, root string) func(since time.Time) (keepstate.UsageHandoff, bool) {
	return func(since time.Time) (keepstate.UsageHandoff, bool) {
		// Ranked by the handoff's own time, so a newer session with no handoff
		// does not mask an older one that has it.
		st, at, ok := NewestAtRoot(commonDir, root, func(st State) (string, bool) {
			if st.UsageHandoff == nil {
				return "", false
			}
			return st.UsageHandoff.At, true
		})
		if !ok || at.Before(since.Truncate(time.Second)) {
			return keepstate.UsageHandoff{}, false
		}
		return *st.UsageHandoff, true
	}
}
