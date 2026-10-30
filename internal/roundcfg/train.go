package roundcfg

import (
	"fmt"
	"sort"
	"time"

	"github.com/l4ci/rota/internal/config"
)

// TrainFill is the count or age threshold shared by the tick and train nudge.
// Zero thresholds preserve each caller's legacy behavior.
type TrainFill struct {
	Max, Minutes int
}

// TrainFillFromConfig parses the two train thresholds with the strict policy
// FromConfig applies, so a caller that needs only these keys refuses a bad
// value exactly as the tick does without failing on an unrelated round key.
func TrainFillFromConfig(cfg any) (TrainFill, error) {
	var f TrainFill
	var err error
	if f.Max, err = config.Int(cfg, config.RoundTrainMaxKey, 0, config.MaxInt); err != nil {
		return TrainFill{}, err
	}
	if f.Minutes, err = config.Int(cfg, config.RoundTrainFillMinutesKey, 0, config.MaxInt); err != nil {
		return TrainFill{}, err
	}
	return f, nil
}

// OldestReady keeps the oldest members when a batch is trimmed, preserving
// their input order for the existing train order selector. Unknown ages go
// first so a missing legacy clock cannot starve a ready PR.
func OldestReady(targets []string, limit int, since func(string) time.Time) []string {
	if limit <= 0 || len(targets) <= limit {
		return targets
	}
	indices := make([]int, len(targets))
	for i := range indices {
		indices[i] = i
	}
	if since != nil {
		sort.SliceStable(indices, func(i, j int) bool {
			return since(targets[indices[i]]).Before(since(targets[indices[j]]))
		})
	}
	selected := make([]bool, len(targets))
	for _, i := range indices[:limit] {
		selected[i] = true
	}
	out := make([]string, 0, limit)
	for i, target := range targets {
		if selected[i] {
			out = append(out, target)
		}
	}
	return out
}

func (f TrainFill) Enabled() bool { return f.Max > 0 || f.Minutes > 0 }

// Wait describes the observed age alongside the configured time limit.
func (f TrainFill) Wait(oldest, now time.Time) string {
	if oldest.IsZero() {
		return "oldest wait unknown"
	}
	minutes := max(int64(0), int64(now.Sub(oldest)/time.Minute))
	if f.Minutes > 0 {
		return fmt.Sprintf("oldest waited %dm of %dm", minutes, f.Minutes)
	}
	return fmt.Sprintf("oldest waited %dm; no time limit", minutes)
}

// Holding is the non-actionable status of a base still filling its train.
func (f TrainFill) Holding(base string, count int, oldest, now time.Time) string {
	ready := fmt.Sprintf("%d ready", count)
	if f.Max > 0 {
		ready = fmt.Sprintf("%d/%d ready", count, f.Max)
	}
	return fmt.Sprintf("holding train for %s: %s, %s", base, ready, f.Wait(oldest, now))
}

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
