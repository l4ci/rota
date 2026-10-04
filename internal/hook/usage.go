package hook

import (
	"encoding/json"
	"fmt"
	"time"
)

// UsageHandoff is the marker a usage block leaves in the session file (D4,
// #206): which window tripped, how full it was and when it resets. The
// supervisor reads it to tell a usage handoff from a context one.
type UsageHandoff struct {
	Window   string  `json:"window"`
	UsedPct  float64 `json:"usedPct"`
	ResetsAt string  `json:"resetsAt,omitempty"`
	At       string  `json:"at"`
}

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
			resetsAt = time.Unix(int64(*c.w.Resets), 0).UTC().Format(time.RFC3339)
		}
	}
	return
}

func usageReason(m UsageHandoff, threshold int, path string) string {
	reset := ""
	if m.ResetsAt != "" {
		reset = ", resets " + m.ResetsAt
	}
	return fmt.Sprintf("Usage is at %s%% of the %s window%s, at or above the switch threshold of %d%%. "+
		"The next orchestrator session may run under another account. "+
		"Write the handoff for it to %s now: first line %q, "+
		"second line \"<!-- written <UTC timestamp, YYYY-MM-DDTHH:MM:SSZ> -->\", then the body from "+
		"references/handoff-template.md with a \"## Round state\" section (slots, who holds what, merges pending, escalations open). "+
		"Commit nothing you do not own. Then run /exit.",
		trimPct(m.UsedPct), m.Window, reset, threshold, path, HandoffMarker)
}
