package hook

import (
	"encoding/json"
	"testing"
	"time"
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
