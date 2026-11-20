package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/keepstate"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/stamp"
)

// StateTTL is how long an untouched session state file lives.
const StateTTL = 24 * time.Hour

// sweepEvery is the least time between two sweeps of the session directory.
const sweepEvery = 10 * time.Minute

// lockWait bounds the state lock: a statusline must never hang.
const lockWait = 2 * time.Second

// State is <git-common-dir>/rota/session/<session_id>.json. D2 and D3 read it.
type State struct {
	SessionID  string          `json:"sessionId"`
	Cwd        string          `json:"cwd"`
	UpdatedAt  string          `json:"updatedAt"`
	ContextPct *float64        `json:"contextPct,omitempty"`
	RateLimits json.RawMessage `json:"rateLimits,omitempty"`
	Raw        json.RawMessage `json:"raw"`

	// Each Stop rule owns its handoff budget, preserved across refreshes.
	OrchestratorHandoff HandoffState `json:"orchestratorHandoff"`
	GuardHandoff        HandoffState `json:"guardHandoff"`

	// GuardWarned is set once the context guard has warned a non-orchestrator
	// session, and cleared when the reading falls below the threshold.
	GuardWarned bool `json:"guardWarned,omitempty"`

	// UsageHandoff is set when the last Stop block was for usage (D4).
	UsageHandoff *keepstate.UsageHandoff `json:"usageHandoff,omitempty"`
}

// HandoffState is one role's block history. Orchestrators count re-entered
// stops for the session; guards count every block in a threshold crossing.
type HandoffState struct {
	HandoffBlocks int    `json:"handoffBlocks,omitempty"`
	BlockedAt     string `json:"blockedAt,omitempty"`
	HandoffFailed bool   `json:"handoffFailed,omitempty"`
}

// UnmarshalJSON also accepts the old unowned, top-level counters. Their role
// was not recorded, so seed both budgets to preserve the active role's history.
// New records take precedence and subsequent writes store only owned budgets.
func (s *State) UnmarshalJSON(data []byte) error {
	type state State // avoid invoking this method recursively
	var wire struct {
		state
		HandoffState
		OrchestratorHandoff *HandoffState `json:"orchestratorHandoff"`
		GuardHandoff        *HandoffState `json:"guardHandoff"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	next := State(wire.state)
	next.OrchestratorHandoff, next.GuardHandoff = wire.HandoffState, wire.HandoffState
	if wire.OrchestratorHandoff != nil {
		next.OrchestratorHandoff = *wire.OrchestratorHandoff
	}
	if wire.GuardHandoff != nil {
		next.GuardHandoff = *wire.GuardHandoff
	}
	*s = next
	return nil
}

var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// StatePath is the state file of a session under a git common dir. The id
// comes from outside, so anything that could leave the directory is refused.
func StatePath(commonDir, sessionID string) (string, error) {
	if !sessionIDRe.MatchString(sessionID) || strings.Contains(sessionID, "..") {
		return "", fmt.Errorf("unusable session id %q", sessionID)
	}
	return filepath.Join(rotastate.SessionDir(commonDir), sessionID+".json"), nil
}

// ReadState loads a state file. found is false for a missing file; a file
// that does not parse is an error.
func ReadState(path string) (State, bool, error) {
	return fsio.ReadJSON[State](path)
}

// Sessions is every readable session state file under a git common dir.
// Unreadable or malformed files are skipped: callers look for the newest
// evidence and a damaged file is none.
func Sessions(commonDir string) []State {
	dir := rotastate.SessionDir(commonDir)
	ents, _ := os.ReadDir(dir)
	var out []State
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if st, found, err := ReadState(filepath.Join(dir, e.Name())); err == nil && found {
			out = append(out, st)
		}
	}
	return out
}

// NewestAtRoot is the session at root with the newest time at reports. at
// returns the RFC3339 time that ranks a session, and false to skip it: the
// callers rank by different fields (UpdatedAt for limits, UsageHandoff.At for
// keepalive), so a session that lacks the field must not mask an older one
// that has it. A session whose cwd is another directory, or whose time does
// not parse, is skipped; a tie keeps the first one read.
func NewestAtRoot(commonDir, root string, at func(State) (string, bool)) (best State, bestAt time.Time, ok bool) {
	for _, st := range Sessions(commonDir) {
		if !fsio.SameDir(st.Cwd, root) {
			continue
		}
		raw, use := at(st)
		if !use {
			continue
		}
		t, err := stamp.Parse(raw)
		if err != nil {
			continue
		}
		if !ok || t.After(bestAt) {
			best, bestAt, ok = st, t, true
		}
	}
	return best, bestAt, ok
}

func writeState(path string, st State) error {
	return fsio.WriteJSONAtomic(path, st)
}

// UpdateState is a locked read-modify-write of one state file. mutate sees
// the current state (found false and zero when missing or unreadable).
func UpdateState(path string, mutate func(st State, found bool) State) error {
	return fsio.Locked(path, lockWait, func() error {
		st, found, _ := ReadState(path)
		return writeState(path, mutate(st, found))
	})
}

// ContextPct derives the context percentage of a statusline payload:
// context_window.used_percentage when present, else current_usage tokens
// (input, cache creation, cache read) over context_window_size. nil when
// neither exists.
func ContextPct(payload map[string]any) *float64 {
	cw, _ := payload["context_window"].(map[string]any)
	if cw == nil {
		return nil
	}
	if p, ok := num(cw["used_percentage"]); ok {
		return &p
	}
	size, ok := num(cw["context_window_size"])
	usage, _ := cw["current_usage"].(map[string]any)
	if !ok || size <= 0 || usage == nil {
		return nil
	}
	sum, have := 0.0, false
	for _, k := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens"} {
		if n, ok := num(usage[k]); ok {
			sum += n
			have = true
		}
	}
	if !have {
		return nil
	}
	p := math.Round(sum/size*1000) / 10
	return &p
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// Dump records one statusline refresh: it parses payload, merges it into the
// session's state file (the Stop hook's counters survive) and sweeps state
// files untouched for StateTTL (by mtime, at most every sweepEvery). An error means nothing useful was written.
func Dump(commonDir string, payload Payload, now time.Time) error {
	id := payload.SessionID
	path, err := StatePath(commonDir, id)
	if err != nil {
		return err
	}
	var raw bytes.Buffer
	if err := json.Compact(&raw, bytes.TrimSpace(payload.Raw)); err != nil {
		return err
	}
	in, cwd := payload.Fields, payload.Cwd
	var rl json.RawMessage
	if v, ok := in["rate_limits"]; ok && v != nil {
		rl, _ = json.Marshal(v)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	err = UpdateState(path, func(st State, _ bool) State {
		st.SessionID, st.Cwd = id, cwd
		st.UpdatedAt = stamp.Format(now)
		st.ContextPct = ContextPct(in)
		st.RateLimits = rl
		st.Raw = json.RawMessage(raw.Bytes())
		return st
	})
	sweep(filepath.Dir(path), id, now)
	return err
}

// sweepMarker is the file whose mtime records the last sweep. It does not end
// in .json, so the session readers never see it.
const sweepMarker = ".sweep"

// sweep removes state files whose mtime is more than StateTTL old, and recall
// receipts more than RecallTTL old, together with their lock sidecars. Held session locks are skipped.
// Every refresh and Stop write rewrites the state, so the mtime
// tracks the last touch and no file is decoded. It runs at most once per
// sweepEvery: a normal refresh pays one stat. Errors are ignored: it is
// housekeeping.
func sweep(dir, keepID string, now time.Time) {
	marker := filepath.Join(dir, sweepMarker)
	if fi, err := os.Stat(marker); err == nil {
		if age := now.Sub(fi.ModTime()); age >= 0 && age < sweepEvery {
			return
		}
	}
	// Claim the slot before scanning so a crash cannot make every refresh rescan.
	if os.WriteFile(marker, nil, 0o666) != nil {
		return
	}
	os.Chtimes(marker, now, now)
	sweepDir(dir, keepID, StateTTL, now)
	sweepDir(filepath.Join(dir, recallDirName), keepID, RecallTTL, now)
}

// sweepDir removes the .json files of dir untouched for more than ttl, except
// keepID's.
func sweepDir(dir, keepID string, ttl time.Duration, now time.Time) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || name == keepID+".json" {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(fi.ModTime()) > ttl {
			sweepSession(filepath.Join(dir, name), ttl, now)
		}
	}
}

func sweepSession(path string, ttl time.Duration, now time.Time) {
	lockPath := fsio.LockPath(path)
	release, ok, err := fsio.TryLock(lockPath)
	if err != nil || !ok {
		return
	}
	defer release()
	// A refresh may have completed since the directory scan. Check the
	// current state while holding its lock before deciding it is expired.
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		os.Remove(lockPath)
		return
	}
	if err != nil || now.Sub(fi.ModTime()) <= ttl {
		return
	}
	if os.Remove(path) == nil {
		// This ends the critical section. A waiter on the old inode detects
		// the removed sidecar and reopens it before writing (fsio.Locked).
		os.Remove(lockPath)
	}
}
