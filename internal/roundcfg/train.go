package roundcfg

import (
	"fmt"
	"time"
)

// TrainFill is the count or age threshold shared by the tick and train nudge.
// Zero thresholds preserve each caller's legacy behavior.
type TrainFill struct {
	Max, Minutes int
}

func (f TrainFill) Enabled() bool { return f.Max > 0 || f.Minutes > 0 }

// Due names the rule that fired, or returns empty while the train is filling.
// Missing timestamps cannot fire the age rule. Count wins when both are due.
func (f TrainFill) Due(count int, oldest, now time.Time) string {
	if count == 0 {
		return ""
	}
	if f.Max > 0 && count >= f.Max {
		return fmt.Sprintf("round.trainMax reached (%d PRs)", f.Max)
	}
	// Compare minutes without converting a user-supplied int to a duration,
	// which could overflow for a large configured threshold.
	if f.Minutes > 0 && !oldest.IsZero() && now.Sub(oldest).Minutes() >= float64(f.Minutes) {
		return fmt.Sprintf("round.trainFillMinutes reached (%d minutes)", f.Minutes)
	}
	return ""
}
