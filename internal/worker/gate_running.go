package worker

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/l4ci/rota/internal/fsio"
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
// dead process is replaced. The check and the write share one short lock, so
// two gates starting together cannot both claim.
func claimGateRun(common, key string) (other int, release func(), err error) {
	path := rotastate.File(common, "gate-"+key)
	me := os.Getpid()
	err = fsio.Locked(path, fsio.LockTimeout, func() error {
		if b, rerr := os.ReadFile(path + ".pid"); rerr == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && pid != me && pidAlive(pid) {
				other = pid
				return nil
			}
		}
		return os.WriteFile(path+".pid", []byte(fmt.Sprintf("%d\n", me)), 0o644)
	})
	if err != nil || other != 0 {
		return other, nil, err
	}
	return 0, func() { os.Remove(path + ".pid") }, nil
}

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
