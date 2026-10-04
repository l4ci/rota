package hook

import (
	"encoding/json"
	"strings"
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

func TestDecideStopUsage(t *testing.T) {
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxAge: 900, HandoffMaxBlks: 2, SwitchOnUsage: true, UsageThreshold: 90}
	st := func(five, seven float64) State {
		return State{UpdatedAt: t0.Add(-10 * time.Second).Format(time.RFC3339), ContextPct: pf(10), RateLimits: rl(five, seven, t0.Unix()+3600)}
	}
	none := Handoff{}
	sup := StopIn{Supervised: true}
	for _, c := range []struct {
		five, seven float64
		block       bool
	}{{89, 10, false}, {90, 10, true}, {10, 89.9, false}, {10, 95, true}} {
		if d := DecideStop(sup, st(c.five, c.seven), set, none, "/h.md", t0); d.Block != c.block {
			t.Errorf("%v/%v: block=%v", c.five, c.seven, d.Block)
		}
	}
	d := DecideStop(sup, st(91, 10), set, none, "/h.md", t0)
	if !strings.Contains(d.Reason, "91%") || !strings.Contains(d.Reason, "five_hour") || !strings.Contains(d.Reason, "90%") || !strings.Contains(d.Reason, "/exit") {
		t.Errorf("reason: %s", d.Reason)
	}
	m := d.State.UsageHandoff
	if m == nil || m.Window != WindowFiveHour || m.UsedPct != 91 || m.At != t0.UTC().Format(time.RFC3339) || m.ResetsAt == "" {
		t.Errorf("marker: %+v", m)
	}

	// Off, unsupervised and held all pass; a hold in the past does not.
	off := set
	off.SwitchOnUsage = false
	if DecideStop(sup, st(95, 95), off, none, "", t0).Block {
		t.Error("blocked with switchOnUsage off")
	}
	if DecideStop(StopIn{}, st(95, 95), set, none, "", t0).Block {
		t.Error("blocked with no supervisor")
	}
	if DecideStop(StopIn{Supervised: true, HoldUntil: t0.Add(time.Minute)}, st(95, 95), set, none, "", t0).Block {
		t.Error("blocked during a hold")
	}
	if !DecideStop(StopIn{Supervised: true, HoldUntil: t0.Add(-time.Minute)}, st(95, 95), set, none, "", t0).Block {
		t.Error("a lapsed hold still held")
	}

	// A context-only block leaves no usage marker, and a hold does not stop it.
	ctx := st(10, 10)
	ctx.ContextPct = pf(80)
	d = DecideStop(StopIn{Supervised: true, HoldUntil: t0.Add(time.Minute)}, ctx, set, none, "/h.md", t0)
	if !d.Block || d.State.UsageHandoff != nil || !strings.Contains(d.Reason, "Context is at") {
		t.Errorf("context block: %+v", d)
	}
	// No rateLimits (API billing) passes.
	noRL := st(0, 0)
	noRL.RateLimits = nil
	if DecideStop(sup, noRL, set, none, "", t0).Block {
		t.Error("blocked with no rate limits")
	}
}
