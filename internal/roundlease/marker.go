package roundlease

// The watch marker: `rota round watch` leaves it next to the lease while it
// runs. The Stop hook reads it to refuse an idle orchestrator without a watch
// and the prompt hook shows whether one is armed. It lives here, beside Env,
// so the hooks read it without importing the watcher or the autopilot.

import (
	"fmt"
	"time"

	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/stamp"
)

// MarkerFile is the watch marker under <git-common-dir>/rota/.
const MarkerFile = "round-watch.json"

// Marker is the marker document: who is watching, since when, how often it
// reports back regardless.
type Marker struct {
	PID       int    `json:"pid"`
	Start     uint64 `json:"start,omitempty"`
	Host      string `json:"host"`
	StartedAt string `json:"startedAt"`
	Heartbeat int    `json:"heartbeatSeconds"`
	// Autopilot is set when the watch runs the round tick (--autopilot), which
	// trains reviewed PRs itself; a plain watch leaves it false.
	Autopilot bool `json:"autopilot,omitempty"`
}

// MarkerPath is the marker file under a common dir.
func MarkerPath(commonDir string) string { return rotastate.File(commonDir, MarkerFile) }

// Armed reports the live watch of the repo, if any. A marker whose process is
// gone, or on another host, is not armed: nothing can be checked about it.
func Armed(env Env, commonDir string) (Marker, bool) {
	return markerFile(commonDir).ReadLive(func(m Marker) bool { return env.Running(m.Host, m.PID, m.Start) })
}

// OwnerPID implements pidlive.Record.
func (m Marker) OwnerPID() int { return m.PID }

func markerFile(commonDir string) pidlive.Marker[Marker] {
	return pidlive.Marker[Marker]{Path: MarkerPath(commonDir), Codec: pidlive.JSON[Marker]()}
}

// ArmedError is returned by Arm when another watch is live.
type ArmedError struct{ Marker Marker }

func (e *ArmedError) Error() string {
	return fmt.Sprintf("a watch is already armed for this repo (pid %d, since %s)", e.Marker.PID, e.Marker.StartedAt)
}

// Arm writes the marker for the calling process and returns the function that
// removes it. Another live watch is an *ArmedError and nothing is written: one
// watch at a time keeps every wake-up a single event.
func Arm(env Env, commonDir string, pid int, heartbeat time.Duration, autopilot bool) (release func(), err error) {
	file := markerFile(commonDir)
	m := Marker{PID: pid, StartedAt: stamp.Format(env.Now()), Heartbeat: int(heartbeat.Seconds()), Autopilot: autopilot}
	m.Host, m.Start = env.Identity(pid)
	live := func(m Marker) bool { return env.Running(m.Host, m.PID, m.Start) }
	cur, held, err := file.Claim(m, live)
	if err != nil {
		return nil, err
	}
	if held {
		return nil, &ArmedError{Marker: cur}
	}
	// Only our own marker: a successor may have replaced a dead one.
	return func() { _ = file.Remove(pid) }, nil
}
