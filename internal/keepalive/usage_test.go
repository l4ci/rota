package keepalive

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/hook"
)

// usageRig is a rig whose first child leaves a usage handoff.
func usageRig(t *testing.T, choice Choice, resets string) (*rig, Env, Options, *[]Decision, *atomic.Bool, *[]bool) {
	r := newRig(t)
	r.script = func(n int, r *rig) Exit {
		if n == 1 {
			r.write("sha-1")
		}
		return Exit{}
	}
	env := r.env(nil)
	var recs []Decision
	var gaps []bool
	gap := new(atomic.Bool)
	env.Gap = gap
	env.UsageMarker = func(since time.Time) (hook.UsageHandoff, bool) {
		return hook.UsageHandoff{Window: hook.WindowFiveHour, UsedPct: 93, ResetsAt: resets, At: since.Format(time.RFC3339)}, true
	}
	env.Choose = func(dir string, thr int) Choice { return choice }
	env.Record = func(d Decision) error { recs = append(recs, d); return nil }
	inner := env.Spawn
	env.Spawn = func(argv, extra []string) (Child, error) {
		gaps = append(gaps, gap.Load())
		return inner(argv, extra)
	}
	o := r.opts()
	o.MaxRestarts = 1
	o.SwitchOnUsage, o.UsageThreshold, o.HoldFallback = true, 90, 30*time.Minute
	o.ConfigDir, o.Account = "/cfg/a", "a"
	return r, env, o, &recs, gap, &gaps
}

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestUsageHandoffSwitchesAccount(t *testing.T) {
	r, env, o, recs, gap, gaps := usageRig(t, Choice{To: &Target{Name: "b", ConfigDir: "/cfg/b", Headroom: 60}}, "")
	res, err := Run(env, o)
	if err != nil || res.Switches != 1 || len(r.starts) != 2 {
		t.Fatalf("%+v %v starts=%d", res, err, len(r.starts))
	}
	if hasEnv(r.envs[0], "CLAUDE_CONFIG_DIR=/cfg/b") || !hasEnv(r.envs[1], "CLAUDE_CONFIG_DIR=/cfg/b") {
		t.Errorf("envs: %v", r.envs)
	}
	if got := r.starts[1][len(r.starts[1])-1]; got != "go on" {
		t.Errorf("restart prompt: %q", got)
	}
	st := r.state()
	if st.Account != "b" || st.Switches != 1 || st.SwitchHold != nil {
		t.Errorf("state: %+v", st)
	}
	if len(*recs) != 1 || (*recs)[0].Action != ActionSwitch || (*recs)[0].From != "a" || (*recs)[0].To != "b" {
		t.Errorf("records: %+v", *recs)
	}
	// The gap opens when a child exits and closes once the next one is started.
	if (*gaps)[0] || !(*gaps)[1] {
		t.Errorf("gap seen at each spawn: %v", *gaps)
	}
	if !gap.Load() {
		t.Error("the gap must be open once the last child has exited")
	}
}

func TestUsageHandoffWithNoAccountRestartsAndHolds(t *testing.T) {
	resets := t0.Add(2 * time.Hour).Format(time.RFC3339)
	r, env, o, recs, _, _ := usageRig(t, Choice{Others: []string{"b: cooling"}}, resets)
	res, err := Run(env, o)
	if err != nil || res.Switches != 0 || len(r.starts) != 2 {
		t.Fatalf("%+v %v starts=%d", res, err, len(r.starts))
	}
	for _, e := range r.envs {
		for _, kv := range e {
			if len(kv) > 17 && kv[:17] == "CLAUDE_CONFIG_DIR" {
				t.Errorf("same account must keep the inherited dir: %v", e)
			}
		}
	}
	st := r.state()
	if st.Account != "a" || st.SwitchHold == nil || st.SwitchHold.Until != resets || st.SwitchHold.Window != hook.WindowFiveHour {
		t.Errorf("state: %+v", st)
	}
	d := (*recs)[0]
	if d.Action != ActionRestart || d.HoldUntil != resets || d.From != "a" {
		t.Errorf("record: %+v", d)
	}
}

func TestUsageHoldFallbackAndPastReset(t *testing.T) {
	_, env, o, recs, _, _ := usageRig(t, Choice{}, "")
	if _, err := Run(env, o); err != nil {
		t.Fatal(err)
	}
	if (*recs)[0].HoldUntil == "" {
		t.Errorf("no reset time must hold for the fallback: %+v", (*recs)[0])
	}
	past := t0.Add(-time.Hour).Format(time.RFC3339)
	r, env, o, recs, _, _ := usageRig(t, Choice{}, past)
	if _, err := Run(env, o); err != nil {
		t.Fatal(err)
	}
	if (*recs)[0].HoldUntil != "" || r.state().SwitchHold != nil {
		t.Errorf("a reset already past must not hold: %+v", (*recs)[0])
	}
}

func TestNoUsageMarkerOrSwitchOffRestartsAsBefore(t *testing.T) {
	r, env, o, recs, _, _ := usageRig(t, Choice{To: &Target{Name: "b", ConfigDir: "/cfg/b"}}, "")
	env.UsageMarker = func(time.Time) (hook.UsageHandoff, bool) { return hook.UsageHandoff{}, false }
	if res, err := Run(env, o); err != nil || res.Switches != 0 || len(*recs) != 0 || len(r.starts) != 2 {
		t.Fatalf("context handoff: %+v %v recs=%d", res, err, len(*recs))
	}
	r, env, o, recs, _, _ = usageRig(t, Choice{To: &Target{Name: "b", ConfigDir: "/cfg/b"}}, "")
	o.SwitchOnUsage = false
	if res, err := Run(env, o); err != nil || res.Switches != 0 || len(*recs) != 0 || hasEnv(r.envs[1], "CLAUDE_CONFIG_DIR=/cfg/b") {
		t.Fatalf("switch off: %+v %v", res, err)
	}
}
