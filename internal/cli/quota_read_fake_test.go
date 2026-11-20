package cli

import (
	"context"
	"time"

	"github.com/l4ci/rota/internal/harness"
)

// claudeReading is a fake Claude quota reader: an available snapshot of the
// 5-hour window at five percent used, resetting at fiveResets (nil: nothing
// spent), and the weekly window at seven percent.
func claudeReading(five float64, fiveResets *time.Time, seven float64) func(context.Context, harness.QuotaReporter, harness.QuotaRequest, harness.QuotaOptions) harness.QuotaSnapshot {
	fiveMins, sevenMins := 300, 10080
	return func(context.Context, harness.QuotaReporter, harness.QuotaRequest, harness.QuotaOptions) harness.QuotaSnapshot {
		return harness.QuotaSnapshot{Status: "available", Windows: []harness.QuotaWindow{
			{DurationMins: &fiveMins, UsedPercent: &five, ResetsAt: fiveResets, Status: "available"},
			{DurationMins: &sevenMins, UsedPercent: &seven, Status: "available"},
		}}
	}
}
