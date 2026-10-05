package roundtick

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotastate"
)

// StateFile is the autopilot's memory between ticks, next to the round lease.
const StateFile = "round-autopilot.json"

// State is what one tick hands the next: the merges held for a person, and the
// attention items already reported, so the watch wakes once per item. It
// belongs to one round; another round's state is discarded.
type State struct {
	Round int `json:"round"`
	// Stopped is set while the round winds down: no tick assigns or merges.
	Stopped  bool              `json:"stopped,omitempty"`
	Held     map[string]string `json:"held,omitempty"`
	Reported []string          `json:"reported,omitempty"`
}

func statePath(commonDir string) string { return rotastate.File(commonDir, StateFile) }

// LoadState reads the state; a missing, unreadable or foreign-round file is the
// empty state.
func LoadState(commonDir string, round int) State {
	s := State{Round: round}
	b, err := os.ReadFile(statePath(commonDir))
	if err != nil {
		return s
	}
	var cur State
	if json.Unmarshal(b, &cur) != nil || cur.Round != round {
		return s
	}
	return cur
}

// Apply loads the state into an env.
func (s State) Apply(e *Env) {
	e.Held = s.Held
	e.Reported = map[string]bool{}
	for _, k := range s.Reported {
		e.Reported[k] = true
	}
}

// update reads the state under its lock, applies fn and writes it back, so a
// tick saving its memory cannot undo a wind-down's stop.
func update(commonDir string, round int, fn func(*State)) error {
	p := statePath(commonDir)
	if err := os.MkdirAll(filepath.Dir(p), 0o777); err != nil {
		return err
	}
	return fsio.Locked(p, fsio.LockTimeout, func() error {
		s := LoadState(commonDir, round)
		fn(&s)
		b, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return err
		}
		return fsio.WriteFileAtomic(p, append(b, '\n'))
	})
}

// SaveState writes what a tick leaves behind.
func SaveState(commonDir string, round int, r Result) error {
	return update(commonDir, round, func(s *State) {
		s.Held, s.Reported = r.Held, nil
		for k := range r.Reported {
			s.Reported = append(s.Reported, k)
		}
		sort.Strings(s.Reported)
	})
}

// SetStopped stops or resumes the autopilot of a round: wind-down stops it
// before it re-verifies, and resumes it if the lease is kept.
func SetStopped(commonDir string, round int, stopped bool) error {
	return update(commonDir, round, func(s *State) { s.Stopped = stopped })
}

// ClearState removes the state when the round is over.
func ClearState(commonDir string) { _ = os.Remove(statePath(commonDir)) }
