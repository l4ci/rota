package keepstate

import "time"

// Handoff is what the filesystem says about the handoff file.
type Handoff struct {
	Exists  bool
	ModTime time.Time
}

// Fresh is the freshness rule of every D1 and D2 decision: the handoff exists
// and was written no more than maxAge ago. A consumed handoff (renamed to
// .consumed) does not exist, so it is never fresh.
func (h Handoff) Fresh(maxAge time.Duration, now time.Time) bool {
	return h.Exists && now.Sub(h.ModTime) <= maxAge
}

// UsageHandoff is the marker a usage block leaves in the session file (D4,
// #206): which window tripped, how full it was and when it resets. The
// supervisor reads it to tell a usage handoff from a context one.
type UsageHandoff struct {
	Window   string  `json:"window"`
	UsedPct  float64 `json:"usedPct"`
	ResetsAt string  `json:"resetsAt,omitempty"`
	At       string  `json:"at"`
}
