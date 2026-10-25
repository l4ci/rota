package limits

import (
	"context"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/worker"
)

// AccountSource is what the account pick and the meter lookup read: the
// configured logins of a harness, the metered accounts and the metered pick.
type AccountSource interface {
	Logins(kind string) []string
	Meters(ctx context.Context) []worker.Meter
	Pick(ctx context.Context, exclude []string) (string, bool)
}

// PickAccount is the account a limited session moves to: the metered pick for
// a harness with a usage meter, else the first login not excluded or cooling.
func PickAccount(ctx context.Context, src AccountSource, root, kind, exclude string, now time.Time) (string, bool) {
	if h := harness.Resolve(kind); !h.UsageMeter() {
		return LoggedPick(root, h.Kind(), src.Logins(h.Kind()), exclude, now)
	}
	return src.Pick(ctx, []string{exclude})
}

// MeterReading is one account's meter as a Reading, or the zero Reading.
func MeterReading(ctx context.Context, src AccountSource, account string) Reading {
	for _, m := range src.Meters(ctx) {
		if m.Name == account {
			return ReadingOf(m)
		}
	}
	return Reading{}
}
