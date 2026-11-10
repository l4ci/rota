package worker

import (
	"os"

	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/rotastate"
)

// gateRunKey names the PR a gate works on, so a second gate for it can be
// told from a gate for another PR. The PR number when the target records one,
// else the slot or ref the caller passed.
func gateRunKey(t GateTarget, arg string) string {
	if n := prNumText(t.PR); n != "" {
		return "pr" + n
	}
	if t.Name != "" {
		return "slot-" + t.Name
	}
	return "arg-" + arg
}

// claimGateRun records this process as the gate of key, before it queues for
// the land lock. It returns the pid of another live gate for the same key
// instead when there is one, and the release func otherwise. A marker left by a
// dead process, or by a pid since reused by another process, is replaced. The check and the write share one short lock, so
// two gates starting together cannot both claim.
func claimGateRun(src pidlive.Source, common, key string) (other int, release func(), err error) {
	file := pidlive.Marker[pidlive.Stamp]{Path: rotastate.File(common, "gate-"+key) + ".pid", Codec: pidlive.Text()}
	me := os.Getpid()
	cur, held, err := file.Claim(pidlive.StampOf(src, me), func(s pidlive.Stamp) bool { return s.Live(src) })
	if err != nil || held {
		return cur.PID, nil, err
	}
	return 0, func() { _ = file.Remove(me) }, nil
}
