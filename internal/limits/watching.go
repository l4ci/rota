package limits

import (
	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
)

// WatchFileName is the record of a running watcher under <git-common-dir>/rota/,
// next to the lease. It says which process is acting on the limits list, so
// `rota limit status` can answer "watching" and a second `rota limit watch` does
// not act twice.
const WatchFileName = "limit-watch.json"

// Watching is the content of that file.
type Watching struct {
	PID int `json:"pid"`
	// Host and Start identify the process, so a reused pid is not mistaken for
	// the watcher. Records from before they existed carry neither.
	Host      string `json:"host,omitempty"`
	Start     uint64 `json:"start,omitempty"`
	StartedAt string `json:"startedAt"`
	// Mode is "watch" for `rota limit watch` or "supervisor" for the loop
	// inside `rota keepalive run`.
	Mode string `json:"mode"`
}

// Modes of Watching.
const (
	ModeWatch      = "watch"
	ModeSupervisor = "supervisor"
)

// WatchPath is the file of a repo, by its git common dir.
func WatchPath(commonDir string) string { return rotastate.File(commonDir, WatchFileName) }

// ReadWatching loads the record; found is false when there is none or it does
// not parse.
func ReadWatching(commonDir string) (Watching, bool) {
	return watchMarker(commonDir).Read()
}

// OwnerPID implements pidlive.Record.
func (w Watching) OwnerPID() int { return w.PID }

func watchMarker(commonDir string) pidlive.Marker[Watching] {
	return pidlive.Marker[Watching]{Path: WatchPath(commonDir), Codec: pidlive.JSON[Watching]()}
}

// ActiveWatching is the record of a watcher that is still running: the same
// liveness rule as the other watcher markers, so a reused pid is not one.
func ActiveWatching(env roundlease.Env, commonDir string) (Watching, bool) {
	return watchMarker(commonDir).ReadLive(func(w Watching) bool { return env.Running(w.Host, w.PID, w.Start) })
}

// WriteWatching records the running watcher.
func WriteWatching(commonDir string, w Watching) error {
	return watchMarker(commonDir).Write(w, nil)
}

// RemoveWatching deletes the record when it is still this process's.
func RemoveWatching(commonDir string, pid int) {
	_ = watchMarker(commonDir).Remove(pid)
}
