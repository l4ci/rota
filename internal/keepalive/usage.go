package keepalive

import (
	"fmt"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/hook"
	"github.com/l4ci/rota/internal/limits"
)

// Actions of a usage Decision, the `action` of its limits entry.
const (
	ActionSwitch  = "switch"
	ActionRestart = "restart"
)

// Target is the account a usage handoff moves the orchestrator to.
type Target struct {
	Name, ConfigDir string
	Headroom        float64
}

// Choice is what the account meters say about a usage handoff: a Target, or
// nil when none is usable, with a line per account that was not taken.
type Choice struct {
	To     *Target
	Others []string
}

// Decision is one usage handoff's outcome, for the limits log.
type Decision struct {
	Action    string
	Marker    hook.UsageHandoff
	From, To  string
	Note      string
	HoldUntil string // empty when there is no hold
}

// Hold is `switchHold` in the state file: until then the Stop hook passes on
// usage, so a session with no account to move to works on and D3's
// in-session sleep handles the real limit.
type Hold struct {
	Until  string `json:"until"`
	Window string `json:"window"`
}

// decideUsage applies a usage handoff before a restart: it names the account
// the next child runs under (curDir, st.Account) and records the decision.
func decideUsage(env Env, o Options, st *State, curDir *string, m hook.UsageHandoff, warn func(string)) {
	now := env.Now()
	ch := env.Choose(*curDir, o.UsageThreshold)
	d := Decision{Marker: m, From: st.Account}
	figure := fmt.Sprintf("%g%% of %s, threshold %d%%", m.UsedPct, m.Window, o.UsageThreshold)
	if ch.To != nil {
		*curDir = ch.To.ConfigDir
		st.Account = ch.To.Name
		st.Switches++
		st.SwitchHold = nil
		d.Action, d.To = ActionSwitch, ch.To.Name
		d.Note = fmt.Sprintf("switched to %s (%g%% headroom): %s", ch.To.Name, ch.To.Headroom, figure)
	} else {
		until := now.Add(o.HoldFallback)
		if m.ResetsAt != "" {
			if t, err := time.Parse(time.RFC3339, m.ResetsAt); err == nil {
				until = t
			}
		}
		d.Action = ActionRestart
		d.Note = "no usable account, restarted on the same account: " + figure
		if len(ch.Others) > 0 {
			d.Note += " (" + strings.Join(ch.Others, "; ") + ")"
		}
		if until.After(now) {
			d.HoldUntil = until.UTC().Format(time.RFC3339)
			st.SwitchHold = &Hold{Until: d.HoldUntil, Window: m.Window}
			d.Note += "; switch held until " + d.HoldUntil
		} else {
			st.SwitchHold = nil
			d.Note += "; the reset has passed, no hold"
		}
	}
	if env.Record != nil {
		if err := env.Record(d); err != nil {
			warn("limits not recorded: " + err.Error())
		}
	}
}

// UsageMarker finds the newest usage handoff written at or after since in the
// session files whose cwd is the project root (D4).
func UsageMarker(commonDir, root string) func(since time.Time) (hook.UsageHandoff, bool) {
	return func(since time.Time) (hook.UsageHandoff, bool) {
		var best hook.UsageHandoff
		var bestAt time.Time
		for _, st := range hook.Sessions(commonDir) {
			if st.UsageHandoff == nil || !fsio.SameDir(st.Cwd, root) {
				continue
			}
			at, err := time.Parse(time.RFC3339, st.UsageHandoff.At)
			if err != nil || at.Before(since.Truncate(time.Second)) {
				continue
			}
			if bestAt.IsZero() || at.After(bestAt) {
				best, bestAt = *st.UsageHandoff, at
			}
		}
		return best, !bestAt.IsZero()
	}
}

// UsageRecorder writes a usage decision to the limits log in
// .rota/workers.json: a switch is "switched", a same-account restart
// "resumed".
func UsageRecorder(root string, now func() time.Time) func(Decision) error {
	return func(d Decision) error {
		status := limits.StatusSwitched
		if d.Action == ActionRestart {
			status = limits.StatusResumed
		}
		t := limits.Time(now())
		_, err := limits.Append(root, limits.Entry{
			Session: limits.Orchestrator, Window: d.Marker.Window, Source: limits.SourceData,
			DetectedAt: d.Marker.At, ResetsAt: d.Marker.ResetsAt, Action: d.Action,
			Account: d.From, Status: status, ResolvedAt: t, Note: d.Note,
		})
		return err
	}
}
