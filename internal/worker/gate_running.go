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
// dead process, or by a pid since reused by another process, is replaced. The check and the write share one short lock, so
// two gates starting together cannot both claim.
func claimGateRun(common, key string) (other int, release func(), err error) {
	path := rotastate.File(common, "gate-"+key)
	me := os.Getpid()
	err = fsio.Locked(path, fsio.LockTimeout, func() error {
		if b, rerr := os.ReadFile(path + ".pid"); rerr == nil {
			if pid, start := parseGateMarker(string(b)); pid != 0 && pid != me && markerLive(pid, start) {
				other = pid
				return nil
			}
		}
		return os.WriteFile(path+".pid", []byte(gateMarker(me)), 0o644)
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

// procStartTicks reads the start time of pid (clock ticks since boot) from
// /proc/<pid>/stat. It is false where /proc is absent or unreadable.
func procStartTicks(pid int) (uint64, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return 0, false
	}
	st, err := strconv.ParseUint(f[19], 10, 64)
	return st, err == nil
}

// gateMarker is the marker body: the pid and, where /proc gives it, the start
// time that tells this process from a later one reusing the pid.
func gateMarker(pid int) string {
	if st, ok := procStartTicks(pid); ok {
		return fmt.Sprintf("%d %d\n", pid, st)
	}
	return fmt.Sprintf("%d\n", pid)
}

// parseGateMarker returns the pid and start ticks of a marker body. start is 0
// for a marker without one (older rota, or no /proc); pid is 0 when unparsable.
func parseGateMarker(body string) (pid int, start uint64) {
	f := strings.Fields(body)
	if len(f) == 0 {
		return 0, 0
	}
	pid, err := strconv.Atoi(f[0])
	if err != nil {
		return 0, 0
	}
	if len(f) > 1 {
		start, _ = strconv.ParseUint(f[1], 10, 64)
	}
	return pid, start
}

// markerLive reports whether the process a marker names is still that process:
// alive, and when the marker recorded a start time, started at that time.
func markerLive(pid int, start uint64) bool {
	if !pidAlive(pid) {
		return false
	}
	if start == 0 {
		return true
	}
	cur, ok := procStartTicks(pid)
	return !ok || cur == start
}
