package keepalive

import (
	"time"

	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/stamp"
)

// SupervisedHold is the Stop hook's view of keepalive.json, read the way
// `rota keepalive status` does: a supervisor is live when the file says
// running and its pid is alive. The hold is its switchHold when that is still
// ahead of now (D4). It lives here, not in hook, because keepalive imports
// hook.
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
