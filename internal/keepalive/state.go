package keepalive

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
)

// FileName is the state file under <git-common-dir>/rota/, next to the lease.
const FileName = "keepalive.json"

// Status values of State.Status.
const (
	StatusRunning = "running"
	StatusStopped = "stopped"
)

// LastExit is how the child last ended.
type LastExit struct {
	Code   int    `json:"code"`
	Signal string `json:"signal,omitempty"`
	At     string `json:"at"`
}

// State is <git-common-dir>/rota/keepalive.json, rewritten under its own lock on
// every transition. D3's usage-limit log is not here: it is `limits` in
// .rota/workers.json.
type State struct {
	PID            int       `json:"pid"`
	Host           string    `json:"host,omitempty"`
	Start          uint64    `json:"start,omitempty"`
	StartedAt      string    `json:"startedAt"`
	Command        []string  `json:"command"`
	Status         string    `json:"status"`
	Restarts       int       `json:"restarts"`
	NoProgress     int       `json:"noProgress"`
	RunStartedAt   string    `json:"runStartedAt"`
	LastHandoffSha string    `json:"lastHandoffSha,omitempty"`
	LastExit       *LastExit `json:"lastExit,omitempty"`
	StopReason     string    `json:"stopReason,omitempty"`
	Escalation     string    `json:"escalation,omitempty"`

	// D4: the orchestrator's current account (empty when it has no name in
	// work.accounts), how many usage handoffs moved it, and the hold that
	// follows one that found no account to move to.
	Account    string `json:"account,omitempty"`
	Switches   int    `json:"switches"`
	SwitchHold *Hold  `json:"switchHold,omitempty"`
}

// StatePath is the state file of a repo, by its git common dir.
func StatePath(commonDir string) string { return rotastate.File(commonDir, FileName) }

// ReadState loads the state file. found is false for a missing file; a file
// that does not parse is an error.
func ReadState(path string) (st State, found bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, false, nil
	}
	if err != nil {
		return st, false, err
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return st, true, nil
}

// WriteState replaces the state file under its lock.
func WriteState(path string, st State) error {
	if st.Command == nil {
		st.Command = []string{}
	}
	return fsio.WriteMarker(path, st, nil)
}

// Running reports whether the supervisor the state names is still alive: the
// file says running, and its pid passes the shared watcher-liveness rule.
func (st State) Running(env roundlease.Env) bool {
	return st.Status == StatusRunning && env.Running(st.Host, st.PID, st.Start)
}
