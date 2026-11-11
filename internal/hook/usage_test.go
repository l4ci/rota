package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/keepstate"
	"github.com/l4ci/rota/internal/rotastate"
)

func rl(five, seven float64, resets int64) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"five_hour": map[string]any{"used_percentage": five, "resets_at": resets},
		"seven_day": map[string]any{"used_percentage": seven, "resets_at": resets + 86400},
	})
	return b
}

func TestUsageOf(t *testing.T) {
	w, p, r, ok := UsageOf(rl(40, 91, 1790000000))
	if !ok || w != WindowSevenDay || p != 91 || r != time.Unix(1790000000+86400, 0).UTC().Format(time.RFC3339) {
		t.Errorf("%s %v %s %v", w, p, r, ok)
	}
	if w, _, _, _ := UsageOf(rl(50, 50, 1)); w != WindowFiveHour {
		t.Errorf("tie went to %s", w)
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`nope`)} {
		if _, _, _, ok := UsageOf(raw); ok {
			t.Errorf("%s read as a figure", raw)
		}
	}
}

// sessionFile writes a hook session state under commonDir.
func sessionFile(t *testing.T, commonDir, id, cwd string, uh *keepstate.UsageHandoff) {
	t.Helper()
	path := filepath.Join(rotastate.SessionDir(commonDir), id+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	err := UpdateState(path, func(st State, _ bool) State {
		st.SessionID, st.Cwd, st.UsageHandoff = id, cwd, uh
		return st
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUsageMarkerPicksNewestForRootAfterSince(t *testing.T) {
	cd, root, other := t.TempDir(), t.TempDir(), t.TempDir()
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return base.Add(d).Format(time.RFC3339) }
	mk := func(w, when string) *keepstate.UsageHandoff {
		return &keepstate.UsageHandoff{Window: w, UsedPct: 90, At: when}
	}
	sessionFile(t, cd, "old", root, mk("old", at(-time.Hour)))
	sessionFile(t, cd, "mid", root, mk("mid", at(time.Minute)))
	sessionFile(t, cd, "new", root, mk("new", at(2*time.Minute)))
	sessionFile(t, cd, "elsewhere", other, mk("elsewhere", at(5*time.Minute)))
	sessionFile(t, cd, "none", root, nil)

	got, ok := UsageMarker(cd, root)(base)
	if !ok || got.Window != "new" {
		t.Fatalf("marker = %+v, %v; want window new", got, ok)
	}
	if _, ok := UsageMarker(cd, root)(base.Add(10 * time.Minute)); ok {
		t.Fatal("marker found though every handoff is before since")
	}
}
