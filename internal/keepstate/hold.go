package keepstate

import (
	"time"

	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/stamp"
)

// SupervisedHold is the Stop hook's view of keepalive.json, read the way
// `rota keepalive status` does: a supervisor is live when the file says
// running and its pid is alive. The hold is its switchHold when that is still
// ahead of now (D4). It lives in this leaf so the Stop hook and keepalive both
// read it without importing each other.
func SupervisedHold(le roundlease.Env, commonDir string, now time.Time) (supervised bool, until time.Time) {
	ks, found, err := ReadState(StatePath(commonDir))
	if err != nil || !found || !ks.Running(le) {
		return false, time.Time{}
	}
	if ks.SwitchHold != nil {
		if t, err := stamp.Parse(ks.SwitchHold.Until); err == nil && t.After(now) {
			until = t
		}
	}
	return true, until
}

// Hold is `switchHold` in the state file: until then the Stop hook passes on
// usage, so a session with no account to move to works on and D3's
// in-session sleep handles the real limit.
type Hold struct {
	Until  string `json:"until"`
	Window string `json:"window"`
}
