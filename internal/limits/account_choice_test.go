package limits_test

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/usagelimit"
	"github.com/l4ci/rota/internal/worker"
)

// One table of meters pins every first-choice path to the same account: the
// dispatch pick (SelectAccount, which Accounts.PickFrom wraps), the pool choice
// (ChoosePool with no current account, margin 0 and equal load) and, where the
// meters carry no headroom, the logged-login pick. Per-algorithm tests do not
// repeat these rows.
var tableNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestAccountChoiceTable(t *testing.T) {
	free := func(name string, h float64) usagelimit.Meter {
		return usagelimit.Meter{Name: name, Verdict: usagelimit.VerdictFree, Headroom: &h}
	}
	unknown := func(name string) usagelimit.Meter {
		return usagelimit.Meter{Name: name, Verdict: usagelimit.VerdictUnknown}
	}
	cooling := func(name string) usagelimit.Meter {
		return usagelimit.Meter{Name: name, Verdict: usagelimit.VerdictCooling}
	}
	for _, tc := range []struct {
		name   string
		meters []usagelimit.Meter
		want   string // "" = nobody is eligible
	}{
		{"empty", nil, ""},
		{"greatest headroom wins", []usagelimit.Meter{free("a", 30), free("b", 80), free("c", 50)}, "b"},
		{"ties keep configured order", []usagelimit.Meter{free("b", 80), free("a", 80)}, "b"},
		{"all unknown: first", []usagelimit.Meter{unknown("a"), unknown("b")}, "a"},
		{"known beats unknown, even at 0", []usagelimit.Meter{unknown("a"), free("b", 0)}, "b"},
		{"unknown never displaces known", []usagelimit.Meter{free("a", 0), unknown("b")}, "a"},
		{"cooling is skipped", []usagelimit.Meter{cooling("a"), free("b", 1)}, "b"},
		{"cooling first, unknown second", []usagelimit.Meter{cooling("a"), unknown("b")}, "b"},
		{"all cooling", []usagelimit.Meter{cooling("a"), cooling("b")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, ok, _ := usagelimit.SelectAccount(tc.meters, usagelimit.AccountSelection{})
			if m.Name != tc.want || ok != (tc.want != "") {
				t.Errorf("SelectAccount = %q, %v; want %q", m.Name, ok, tc.want)
			}
			var pool []worker.PoolEntry
			for i, m := range tc.meters {
				pool = append(pool, worker.PoolEntry{Kind: "claude", Name: m.Name, Meter: m, Order: i})
			}
			c, ok := worker.ChoosePool(pool, nil, 0)
			if c.Entry.Name != tc.want || ok != (tc.want != "") {
				t.Errorf("ChoosePool = %q, %v; want %q", c.Entry.Name, ok, tc.want)
			}
			if tc.want == "" {
				return
			}
			// LoggedPick reads no headroom: it agrees only on meters without one.
			for _, m := range tc.meters {
				if m.Headroom != nil {
					return
				}
			}
			var logins []string
			var entries []limits.Entry
			for _, m := range tc.meters {
				logins = append(logins, m.Name)
				if m.Verdict == usagelimit.VerdictCooling {
					entries = append(entries, limits.Entry{Kind: "codex", Account: m.Name, Status: limits.StatusWaiting, ResetsAt: limits.Time(tableNow.Add(time.Hour))})
				}
			}
			if got, ok := limits.LoggedPick(entries, "codex", logins, "", tableNow); got != tc.want || !ok {
				t.Errorf("LoggedPick = %q, %v; want %q", got, ok, tc.want)
			}
		})
	}
}
