package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func guardState(pct float64, updated string) State {
	return State{UpdatedAt: updated, ContextPct: &pct}
}

// applyRule exercises decisions through their persisted session and a real
// handoff file. Every state-changing case also checks the stored result.
func applyRule(t *testing.T, action Action, in StopIn, st State, set Settings, ho Handoff, now time.Time) SessionDecision {
	t.Helper()
	cd := t.TempDir()
	path, err := StatePath(cd, "session")
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateState(path, func(State, bool) State { return st }); err != nil {
		t.Fatal(err)
	}
	handoff := filepath.Join(cd, "h.md")
	if ho.Exists {
		if err := os.WriteFile(handoff, []byte("handoff"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(handoff, ho.ModTime, ho.ModTime); err != nil {
			t.Fatal(err)
		}
	}
	d, ok := ApplySession(SessionIn{Action: action, CommonDir: cd, SessionID: "session", HandoffPath: handoff,
		Stop: in, Settings: set, Now: func() time.Time { return now }})
	if !ok {
		t.Fatal("readable session did not reach a decision")
	}
	stored, found, err := ReadState(path)
	if err != nil || !found || !reflect.DeepEqual(stored, d.State) {
		t.Fatalf("stored state %+v, want %+v (found=%v, err=%v)", stored, d.State, found, err)
	}
	return d
}

func applyStop(t *testing.T, in StopIn, st State, set Settings, ho Handoff, now time.Time) StopDecision {
	t.Helper()
	return applyRule(t, StopHandoff, in, st, set, ho, now).StopDecision
}

func applyGuardStop(t *testing.T, st State, set Settings, ho Handoff, now time.Time) StopDecision {
	t.Helper()
	return applyRule(t, GuardStop, StopIn{}, st, set, ho, now).StopDecision
}

func applyGuardPrompt(t *testing.T, st State, set Settings, now time.Time) SessionDecision {
	t.Helper()
	return applyRule(t, GuardPrompt, StopIn{}, st, set, Handoff{}, now)
}

func TestApplySessionPromptPersistsCrossing(t *testing.T) {
	cd := t.TempDir()
	path, err := StatePath(cd, "session")
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateState(path, func(State, bool) State {
		return State{SessionID: "session", UpdatedAt: t0.Format(time.RFC3339), ContextPct: pf(75)}
	}); err != nil {
		t.Fatal(err)
	}
	in := SessionIn{Action: GuardPrompt, CommonDir: cd, SessionID: "session", HandoffPath: filepath.Join(cd, "handoff.md"),
		Settings: Settings{Threshold: 75, StateMaxAge: 120}, Now: func() time.Time { return t0 }}
	d, ok := ApplySession(in)
	if !ok || !strings.Contains(d.Line, "/rota-pause") {
		t.Fatalf("first warning: %+v, ok=%v", d, ok)
	}
	again, ok := ApplySession(in)
	if !ok || again.Line != "" {
		t.Fatalf("warning repeated after persisted crossing: %+v, ok=%v", again, ok)
	}
}

func TestApplySessionUnavailable(t *testing.T) {
	for _, action := range []Action{StopHandoff, GuardStop, GuardPrompt} {
		for _, fixture := range []string{"missing", "malformed", "invalid id", "no handoff"} {
			t.Run(fmt.Sprintf("%d/%s", action, fixture), func(t *testing.T) {
				cd := t.TempDir()
				in := SessionIn{Action: action, CommonDir: cd, SessionID: "session", HandoffPath: filepath.Join(cd, "h.md"),
					Now: func() time.Time { return t0 }}
				path, _ := StatePath(cd, in.SessionID)
				switch fixture {
				case "malformed":
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
						t.Fatal(err)
					}
				case "invalid id":
					in.SessionID = "../escape"
				case "no handoff":
					if action != StopHandoff {
						t.Skip("only the orchestrator requires a resolved path")
					}
					if err := UpdateState(path, func(State, bool) State { return guardState(95, t0.Format(time.RFC3339)) }); err != nil {
						t.Fatal(err)
					}
					in.HandoffPath = ""
				}
				d, ok := ApplySession(in)
				if ok || d.Block || d.Line != "" || d.Persist {
					t.Fatalf("unavailable session acted on: %+v, ok=%v", d, ok)
				}
			})
		}
	}
}

func TestApplySessionKeepsConcurrentRefresh(t *testing.T) {
	for _, action := range []Action{StopHandoff, GuardStop, GuardPrompt} {
		t.Run(fmt.Sprint(action), func(t *testing.T) {
			cd := t.TempDir()
			path, _ := StatePath(cd, "session")
			if err := UpdateState(path, func(State, bool) State {
				return guardState(80, t0.Format(time.RFC3339))
			}); err != nil {
				t.Fatal(err)
			}
			var refreshed State
			d, ok := ApplySession(SessionIn{Action: action, CommonDir: cd, SessionID: "session", HandoffPath: filepath.Join(cd, "h.md"),
				Settings: Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxBlks: 2}, Now: func() time.Time {
					// Refresh after the decision's state read, before its update.
					if err := UpdateState(path, func(cur State, _ bool) State {
						cur.Cwd, cur.UpdatedAt, cur.ContextPct = "/new", t0.Add(time.Second).Format(time.RFC3339), pf(20)
						cur.Raw, cur.RateLimits = json.RawMessage(`{"refresh":true}`), rl(10, 20, 123)
						// Other hook rules own these fields.
						if action == GuardPrompt {
							cur.HandoffBlocks, cur.BlockedAt, cur.HandoffFailed = 7, "other", true
						} else {
							cur.GuardWarned = true
						}
						if action != StopHandoff {
							cur.UsageHandoff = &UsageHandoff{Window: WindowFiveHour, UsedPct: 99}
						}
						return cur
					}); err != nil {
						t.Fatal(err)
					}
					var err error
					refreshed, _, err = ReadState(path)
					if err != nil {
						t.Fatal(err)
					}
					return t0
				}})
			if !ok || !d.Persist {
				t.Fatalf("no persisted decision: %+v, ok=%v", d, ok)
			}
			got, found, err := ReadState(path)
			if err != nil || !found {
				t.Fatalf("read: found=%v, err=%v", found, err)
			}
			if got.Cwd != "/new" || got.UpdatedAt != t0.Add(time.Second).Format(time.RFC3339) || got.ContextPct == nil || *got.ContextPct != 20 || !reflect.DeepEqual(got.Raw, refreshed.Raw) || !reflect.DeepEqual(got.RateLimits, refreshed.RateLimits) {
				t.Fatalf("refresh overwritten: %+v", got)
			}
			if !got.GuardWarned || (action == GuardPrompt && (got.HandoffBlocks != 7 || got.BlockedAt != "other" || !got.HandoffFailed)) || (action != StopHandoff && (got.UsageHandoff == nil || got.UsageHandoff.UsedPct != 99)) {
				t.Fatalf("other hook's fields overwritten: %+v", got)
			}
		})
	}
}

func TestApplySessionStop(t *testing.T) {
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxAge: 900, HandoffMaxBlks: 2}
	st := func(pct float64) State {
		return State{UpdatedAt: t0.Add(-10 * time.Second).Format(time.RFC3339), ContextPct: pf(pct)}
	}
	none := Handoff{}
	for _, c := range []struct {
		pct   float64
		block bool
	}{{74, false}, {74.9, false}, {75, true}, {99, true}} {
		d := applyStop(t, StopIn{}, st(c.pct), set, none, t0)
		if d.Block != c.block {
			t.Errorf("pct %v: block=%v", c.pct, d.Block)
		}
	}
	d := applyStop(t, StopIn{}, st(80), set, none, t0)
	if !strings.Contains(d.Reason, "80%") || !strings.Contains(d.Reason, "75%") || !strings.Contains(d.Reason, "/h.md") || !strings.Contains(d.Reason, "/exit") {
		t.Errorf("reason: %s", d.Reason)
	}
	// A stale reading or a missing percentage passes.
	old := st(99)
	old.UpdatedAt = t0.Add(-3 * time.Minute).Format(time.RFC3339)
	if applyStop(t, StopIn{}, old, set, none, t0).Block {
		t.Error("stale state blocked")
	}
	if applyStop(t, StopIn{}, State{UpdatedAt: st(0).UpdatedAt}, set, none, t0).Block {
		t.Error("no percentage blocked")
	}
	// A fresh handoff passes without a block.
	fresh := Handoff{Exists: true, ModTime: t0.Add(-time.Minute)}
	if applyStop(t, StopIn{}, st(99), set, fresh, t0).Block {
		t.Error("fresh handoff still blocked")
	}
	if !applyStop(t, StopIn{}, st(99), set, Handoff{Exists: true, ModTime: t0.Add(-time.Hour)}, t0).Block {
		t.Error("a day-old handoff must not count")
	}
}

func TestApplySessionStopLoopPrevention(t *testing.T) {
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxAge: 900, HandoffMaxBlks: 2}
	cur := State{UpdatedAt: t0.Format(time.RFC3339), ContextPct: pf(90)}
	// First stop: block, stamp.
	d := applyStop(t, StopIn{}, cur, set, Handoff{}, t0)
	if !d.Block || !d.Persist || d.State.BlockedAt == "" || d.State.HandoffBlocks != 0 {
		t.Fatalf("first: %+v", d)
	}
	cur = d.State
	// Active, handoff written after the block: pass.
	written := Handoff{Exists: true, ModTime: t0.Add(5 * time.Second)}
	if applyStop(t, StopIn{StopHookActive: true}, cur, set, written, t0.Add(10*time.Second)).Block {
		t.Error("active + new handoff should pass")
	}
	// Active, handoff older than the block: not the answer to it.
	older := Handoff{Exists: true, ModTime: t0.Add(-time.Second)}
	if !applyStop(t, StopIn{StopHookActive: true}, cur, set, older, t0.Add(10*time.Second)).Block {
		t.Error("active + older handoff should block")
	}
	// Active, no handoff: block up to the cap, then pass and record failure.
	for i := 1; i <= 2; i++ {
		d = applyStop(t, StopIn{StopHookActive: true}, cur, set, Handoff{}, t0.Add(time.Duration(i)*10*time.Second))
		if !d.Block || d.State.HandoffBlocks != i {
			t.Fatalf("reblock %d: %+v", i, d)
		}
		cur = d.State
	}
	d = applyStop(t, StopIn{StopHookActive: true}, cur, set, Handoff{}, t0.Add(time.Minute))
	if d.Block || !d.State.HandoffFailed || !d.Persist {
		t.Fatalf("cap: %+v", d)
	}
	cur = d.State
	// Failed sessions are never held again, even without stop_hook_active.
	if applyStop(t, StopIn{}, cur, set, Handoff{}, t0.Add(time.Minute)).Block {
		t.Error("failed session blocked again")
	}
}

func TestApplySessionGuardPromptReadings(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxBlks: 2}
	fresh := "2026-10-03T11:59:30Z"
	if d := applyGuardPrompt(t, guardState(74, fresh), set, now); d.Line != "" || d.Persist {
		t.Errorf("below: %+v", d)
	}
	low := guardState(10, fresh)
	low.GuardWarned = true
	if r := applyGuardPrompt(t, low, set, now); r.State.GuardWarned || !r.Persist {
		t.Errorf("reset: %+v", r)
	}
	if s := applyGuardPrompt(t, guardState(99, "2026-10-03T11:50:00Z"), set, now); s.Line != "" {
		t.Errorf("stale: %+v", s)
	}
	if n := applyGuardPrompt(t, State{UpdatedAt: fresh}, set, now); n.Line != "" {
		t.Errorf("no reading: %+v", n)
	}
}

func TestApplySessionGuardStop(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxBlks: 2}
	fresh := "2026-10-03T11:59:30Z"
	st := guardState(80, fresh)
	for i := 1; i <= 2; i++ {
		d := applyGuardStop(t, st, set, Handoff{}, now)
		if !d.Block || !strings.Contains(d.Reason, "/rota-pause") || d.State.HandoffBlocks != i {
			t.Fatalf("block %d: %+v", i, d)
		}
		st = d.State
	}
	d := applyGuardStop(t, st, set, Handoff{}, now)
	if d.Block || !d.State.HandoffFailed {
		t.Fatalf("past the cap: %+v", d)
	}
	st = d.State
	st.ContextPct = func() *float64 { p := 10.0; return &p }()
	if r := applyGuardStop(t, st, set, Handoff{}, now); r.Block || r.State.HandoffFailed || r.State.HandoffBlocks != 0 {
		t.Errorf("reset below the threshold: %+v", r)
	}
	if s := applyGuardStop(t, guardState(99, "2026-10-03T11:50:00Z"), set, Handoff{}, now); s.Block {
		t.Errorf("stale: %+v", s)
	}
}

func TestApplySessionGuardStopFreshHandoff(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	set := Settings{Threshold: 75, StateMaxAge: 120, HandoffMaxAge: 900, HandoffMaxBlks: 2}
	st := guardState(80, "2026-10-03T11:59:30Z")
	d := applyGuardStop(t, st, set, Handoff{}, now)
	if !d.Block {
		t.Fatalf("no note: %+v", d)
	}
	st = d.State
	blocked, _ := time.Parse(time.RFC3339, st.BlockedAt)
	if r := applyGuardStop(t, st, set, Handoff{Exists: true, ModTime: blocked.Add(10 * time.Second)}, now); r.Block {
		t.Errorf("a fresh note written after the block must pass: %+v", r)
	}
	if r := applyGuardStop(t, st, set, Handoff{Exists: true, ModTime: blocked.Add(-time.Minute)}, now); !r.Block {
		t.Errorf("a note older than the block must not pass: %+v", r)
	}
	if r := applyGuardStop(t, st, set, Handoff{Exists: true, ModTime: now.Add(-time.Hour)}, now); !r.Block {
		t.Errorf("a stale note must not pass: %+v", r)
	}
}

func TestApplySessionStopUsage(t *testing.T) {
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
		if d := applyStop(t, sup, st(c.five, c.seven), set, none, t0); d.Block != c.block {
			t.Errorf("%v/%v: block=%v", c.five, c.seven, d.Block)
		}
	}
	d := applyStop(t, sup, st(91, 10), set, none, t0)
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
	if applyStop(t, sup, st(95, 95), off, none, t0).Block {
		t.Error("blocked with switchOnUsage off")
	}
	if applyStop(t, StopIn{}, st(95, 95), set, none, t0).Block {
		t.Error("blocked with no supervisor")
	}
	if applyStop(t, StopIn{Supervised: true, HoldUntil: t0.Add(time.Minute)}, st(95, 95), set, none, t0).Block {
		t.Error("blocked during a hold")
	}
	if !applyStop(t, StopIn{Supervised: true, HoldUntil: t0.Add(-time.Minute)}, st(95, 95), set, none, t0).Block {
		t.Error("a lapsed hold still held")
	}

	// A context-only block leaves no usage marker, and a hold does not stop it.
	ctx := st(10, 10)
	ctx.ContextPct = pf(80)
	d = applyStop(t, StopIn{Supervised: true, HoldUntil: t0.Add(time.Minute)}, ctx, set, none, t0)
	if !d.Block || d.State.UsageHandoff != nil || !strings.Contains(d.Reason, "Context is at") {
		t.Errorf("context block: %+v", d)
	}
	// No rateLimits (API billing) passes.
	noRL := st(0, 0)
	noRL.RateLimits = nil
	if applyStop(t, sup, noRL, set, none, t0).Block {
		t.Error("blocked with no rate limits")
	}
}
