package limits

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// maxResetAhead bounds a reset time read from text: a longer wait is more
// likely a misread than a limit.
const maxResetAhead = 8 * 24 * time.Hour

var resetRe = regexp.MustCompile(`(?i)\b(?:resets?|try again)\s+(?:at\s+|on\s+)?` +
	`(?:(jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)[a-z]*\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s+(?:at\s+)?)?` +
	`(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b(?:\s*\(([A-Za-z_]+(?:/[A-Za-z_+\-0-9]+)*)\))?`)

var months = map[string]time.Month{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}

// ParseReset reads a reset time out of limit text: `resets 3pm`, Codex's
// `try again at 3:42 PM`, `resets at
// 3:30pm (Europe/Berlin)`, `resets at 15:00`, `resets Oct 5, 3pm`. A bare
// time is the next such time after now in loc (or the zone the text names); a
// date is the next such date. It is false when the text has no time, or the
// time lies more than 8 days ahead.
func ParseReset(text string, now time.Time, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = time.Local
	}
	for _, m := range resetRe.FindAllStringSubmatch(text, -1) {
		hour, _ := strconv.Atoi(m[3])
		min := 0
		if m[4] != "" {
			min, _ = strconv.Atoi(m[4])
		}
		ampm := strings.ToLower(m[5])
		switch {
		case ampm == "" && m[4] == "":
			continue // a bare number is not a time
		case ampm != "" && (hour < 1 || hour > 12):
			continue
		case ampm == "pm" && hour < 12:
			hour += 12
		case ampm == "am" && hour == 12:
			hour = 0
		}
		if hour > 23 || min > 59 {
			continue
		}
		l := loc
		if m[6] != "" {
			if zl, err := time.LoadLocation(m[6]); err == nil {
				l = zl
			}
		}
		n := now.In(l)
		var t time.Time
		if m[1] != "" {
			day, _ := strconv.Atoi(m[2])
			t = time.Date(n.Year(), months[strings.ToLower(m[1])], day, hour, min, 0, 0, l)
			if t.Before(n) {
				t = t.AddDate(1, 0, 0)
			}
		} else {
			t = time.Date(n.Year(), n.Month(), n.Day(), hour, min, 0, 0, l)
			if !t.After(n) {
				t = t.AddDate(0, 0, 1)
			}
		}
		if t.After(now) && t.Sub(now) <= maxResetAhead {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// Data is what the orchestrator's session file says about its usage windows.
// Known is false when it has none; Limited says a window is spent with its
// reset still ahead, and Window and ResetsAt then name it.
type Data struct {
	Window   string
	ResetsAt time.Time
	Limited  bool
	Known    bool
}

type rateWindow struct {
	Used   *float64 `json:"used_percentage"`
	Resets *float64 `json:"resets_at"`
}

// DataFromRateLimits reads the `rateLimits` the statusline dump stored (D1):
// `five_hour` and `seven_day`, each with `used_percentage` and `resets_at` in
// epoch seconds. A window at 100 or more with a reset still ahead is a limit;
// with both, the later reset is the one.
func DataFromRateLimits(raw json.RawMessage, now time.Time) Data {
	var d Data
	if len(raw) == 0 {
		return d
	}
	var rl struct {
		Five  *rateWindow `json:"five_hour"`
		Seven *rateWindow `json:"seven_day"`
	}
	if json.Unmarshal(raw, &rl) != nil {
		return d
	}
	for _, w := range []struct {
		name string
		win  *rateWindow
	}{{WindowFiveHour, rl.Five}, {WindowSevenDay, rl.Seven}} {
		if w.win == nil || w.win.Used == nil {
			continue
		}
		d.Known = true
		if *w.win.Used < 100 || w.win.Resets == nil {
			continue
		}
		at := time.Unix(int64(*w.win.Resets), 0).UTC()
		if !at.After(now) {
			continue
		}
		if !d.Limited || at.After(d.ResetsAt) {
			d.Window, d.ResetsAt, d.Limited = w.name, at, true
		}
	}
	return d
}
