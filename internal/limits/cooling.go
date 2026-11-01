package limits

import (
	"time"

	"github.com/l4ci/rota/internal/worker"
)

// Cooling are the accounts of the harness kind whose limit has not reset yet,
// with the reset time. A limit resolved by moving the issue (StatusSwitched)
// counts: the login is just as spent. It is how a Codex login's spent window is
// known: Codex has no usage meter, so the limit message the watcher logged is
// the only evidence. An entry with no account or a reset already past is not
// counted.
func Cooling(root, kind string, now time.Time) map[string]time.Time {
	out := map[string]time.Time{}
	for _, e := range Load(root) {
		if e.Status != StatusWaiting && e.Status != StatusSwitched {
			continue
		}
		at, ok := e.Resets()
		if e.Account == "" || e.Kind != kind || !ok || !at.After(now) {
			continue
		}
		if prev, seen := out[e.Account]; !seen || at.After(prev) {
			out[e.Account] = at
		}
	}
	return out
}

// LoggedPick is the first of the logins of a kind with no usage meter that is
// not excluded and not cooling, in configured order.
func LoggedPick(root, kind string, logins []string, exclude string, now time.Time) (string, bool) {
	cooling := Cooling(root, kind, now)
	meters := make([]worker.Meter, 0, len(logins))
	for _, l := range logins {
		verdict := worker.VerdictUnknown
		if _, spent := cooling[l]; spent {
			verdict = worker.VerdictCooling
		}
		meters = append(meters, worker.Meter{Name: l, Verdict: verdict})
	}
	m, ok, _ := worker.SelectAccount(meters, worker.AccountSelection{Exclude: []string{exclude}})
	return m.Name, ok
}
