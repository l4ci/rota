package keepalive

import (
	"fmt"
	"github.com/l4ci/rota/internal/keepstate"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/limits"
	"github.com/l4ci/rota/internal/stamp"
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
	Marker    keepstate.UsageHandoff
	From, To  string
	Note      string
	HoldUntil string // empty when there is no hold
}

// decideUsage applies a usage handoff before a restart: it names the account
// the next child runs under (curDir, st.Account) and records the decision.
func decideUsage(env Env, sw *Switch, st *keepstate.State, curDir *string, m keepstate.UsageHandoff, warn func(string)) {
	now := env.Now()
	ch := sw.Choose(*curDir, sw.Threshold)
	d := Decision{Marker: m, From: st.Account}
	figure := fmt.Sprintf("%g%% of %s, threshold %d%%", m.UsedPct, m.Window, sw.Threshold)
	if ch.To != nil {
		*curDir = ch.To.ConfigDir
		st.Account = ch.To.Name
		st.Switches++
		st.SwitchHold = nil
		d.Action, d.To = limits.ActionSwitch, ch.To.Name
		d.Note = fmt.Sprintf("switched to %s (%g%% headroom): %s", ch.To.Name, ch.To.Headroom, figure)
	} else {
		until := now.Add(sw.HoldFallback)
		if m.ResetsAt != "" {
			if t, err := stamp.Parse(m.ResetsAt); err == nil {
				until = t
			}
		}
		d.Action = limits.ActionRestart
		d.Note = "no usable account, restarted on the same account: " + figure
		if len(ch.Others) > 0 {
			d.Note += " (" + strings.Join(ch.Others, "; ") + ")"
		}
		if until.After(now) {
			d.HoldUntil = stamp.Format(until)
			st.SwitchHold = &keepstate.Hold{Until: d.HoldUntil, Window: m.Window}
			d.Note += "; switch held until " + d.HoldUntil
		} else {
			st.SwitchHold = nil
			d.Note += "; the reset has passed, no hold"
		}
	}
	if sw.Record != nil {
		if err := sw.Record(d); err != nil {
			warn("limits not recorded: " + err.Error())
		}
	}
}

// UsageRecorder writes a usage decision to the limits log; limits owns the
// action-to-status mapping.
func UsageRecorder(root string, now func() time.Time) func(Decision) error {
	return func(d Decision) error {
		_, err := limits.RecordUsage(root, now(), limits.UsageDecision{
			Action: d.Action, Window: d.Marker.Window, DetectedAt: d.Marker.At,
			ResetsAt: d.Marker.ResetsAt, From: d.From, Note: d.Note,
		})
		return err
	}
}
