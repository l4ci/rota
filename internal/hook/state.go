package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/rotastate"
)

// StateTTL is how long an untouched session state file lives.
const StateTTL = 24 * time.Hour

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

	// Written by the Stop hook, kept across statusline refreshes.
	HandoffBlocks int    `json:"handoffBlocks,omitempty"`
	BlockedAt     string `json:"blockedAt,omitempty"`
	HandoffFailed bool   `json:"handoffFailed,omitempty"`

	// UsageHandoff is set when the last Stop block was for usage (D4).
	UsageHandoff *UsageHandoff `json:"usageHandoff,omitempty"`
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

func writeState(path string, st State) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return fsio.WriteFileAtomic(path, append(b, '\n'))
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
// files untouched for StateTTL. An error means nothing useful was written.
func Dump(commonDir string, payload []byte, now time.Time) error {
	var in map[string]any
	dec := json.NewDecoder(bytes.NewReader(payload))
	if err := dec.Decode(&in); err != nil || in == nil {
		return fmt.Errorf("statusline input is not a JSON object")
	}
	id, _ := in["session_id"].(string)
	path, err := StatePath(commonDir, id)
	if err != nil {
		return err
	}
	var raw bytes.Buffer
	if err := json.Compact(&raw, bytes.TrimSpace(payload)); err != nil {
		return err
	}
	cwd, _ := in["cwd"].(string)
	if ws, _ := in["workspace"].(map[string]any); cwd == "" && ws != nil {
		cwd, _ = ws["current_dir"].(string)
	}
	var rl json.RawMessage
	if v, ok := in["rate_limits"]; ok && v != nil {
		rl, _ = json.Marshal(v)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	err = UpdateState(path, func(st State, _ bool) State {
		st.SessionID, st.Cwd = id, cwd
		st.UpdatedAt = now.UTC().Format(time.RFC3339)
		st.ContextPct = ContextPct(in)
		st.RateLimits = rl
		st.Raw = json.RawMessage(raw.Bytes())
		return st
	})
	sweep(filepath.Dir(path), id, now)
	return err
}

// sweep removes state files whose updatedAt is more than StateTTL old, with
// their lock files. Errors are ignored: it is housekeeping.
func sweep(dir, keepID string, now time.Time) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || name == keepID+".json" {
			continue
		}
		p := filepath.Join(dir, name)
		st, found, err := ReadState(p)
		var at time.Time
		if err == nil && found {
			at, err = time.Parse(time.RFC3339, st.UpdatedAt)
		}
		if err != nil || at.IsZero() {
			fi, serr := os.Stat(p)
			if serr != nil {
				continue
			}
			at = fi.ModTime()
		}
		if now.Sub(at) > StateTTL {
			os.Remove(p)
			os.Remove(p + ".lock")
		}
	}
}
