package hook

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/stamp"
)

// HandoffMarker is the first line of a handoff the Stop hook asked for.
const HandoffMarker = "<!-- rota-handoff: orchestrator -->"

// Handoff is what the filesystem says about the handoff file.
type Handoff struct {
	Exists  bool
	ModTime time.Time
}

// Fresh is the freshness rule of every D1 and D2 decision: the handoff exists
// and was written no more than maxAge ago. A consumed handoff (renamed to
// .consumed) does not exist, so it is never fresh.
func (h Handoff) Fresh(maxAge time.Duration, now time.Time) bool {
	return h.Exists && now.Sub(h.ModTime) <= maxAge
}

// StatHandoff reads the handoff file's presence and mtime.
func StatHandoff(path string) Handoff {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return Handoff{}
	}
	return Handoff{Exists: true, ModTime: fi.ModTime()}
}

// StopIn is the Stop payload's relevant fields.
type StopIn struct {
	StopHookActive bool
	// Supervised is whether a live `rota keepalive run` would restart the
	// orchestrator (D4); without one a usage block would strand it.
	Supervised bool
	// HoldUntil is the end of the supervisor's switch hold, zero for none.
	HoldUntil time.Time
}

// StopDecision is what the Stop hook does. Block false means print nothing.
type StopDecision struct {
	Block   bool
	Reason  string
	State   State // the state to persist; Persist says whether it changed
	Persist bool
}

// DecideStop applies the contract's Stop rules to an orchestrator session's
// state. The caller has already established that the session is the
// orchestrator and found a readable state file (st). handoffPath only
// appears in the block reason.
func DecideStop(in StopIn, st State, set Settings, ho Handoff, handoffPath string, now time.Time) StopDecision {
	d := StopDecision{State: st}
	if st.HandoffFailed {
		return d // gave up already: never hold the session again
	}
	at, err := stamp.Parse(st.UpdatedAt)
	if err != nil || now.Sub(at) > time.Duration(set.StateMaxAge)*time.Second {
		return d // a stale reading is not acted on
	}
	ctxTrip := st.ContextPct != nil && *st.ContextPct >= float64(set.Threshold)
	var usage *UsageHandoff
	if set.SwitchOnUsage && in.Supervised && !now.Before(in.HoldUntil) {
		if w, p, r, ok := UsageOf(st.RateLimits); ok && p >= float64(set.UsageThreshold) {
			usage = &UsageHandoff{Window: w, UsedPct: p, ResetsAt: r, At: stamp.Format(now)}
		}
	}
	if !ctxTrip && usage == nil {
		return d
	}
	if !holdForHandoff(&d, set, ho, now, handoffRule{reentered: in.StopHookActive}) {
		return d
	}
	if usage != nil {
		d.State.UsageHandoff = usage
		d.Reason = usageReason(*usage, set.UsageThreshold, handoffPath)
	} else {
		d.State.UsageHandoff = nil
		d.Reason = blockReason(*st.ContextPct, set.Threshold, handoffPath)
	}
	return d
}

// handoffRule is where the two Stop deciders differ in the shared block step.
type handoffRule struct {
	// reentered is a Stop re-entered after a block (stop_hook_active): a
	// handoff newer than the last block passes even once it has aged, and the
	// block is counted.
	reentered bool
	// countAll counts every block, so a first stop is held no more than a
	// re-entered one (the context guard).
	countAll bool
}

// holdForHandoff is the block rule both Stop deciders share, run once their
// own trip condition has fired. It passes when the handoff is written, latches
// HandoffFailed at the cap, and otherwise stamps the block into d.State and
// returns true; the caller then sets Reason. d.Persist is set whenever d.State
// changed.
func holdForHandoff(d *StopDecision, set Settings, ho Handoff, now time.Time, r handoffRule) bool {
	var blockedAt time.Time
	if d.State.BlockedAt != "" {
		blockedAt, _ = stamp.Parse(d.State.BlockedAt)
	}
	if ho.Exists {
		newer := !ho.ModTime.Before(blockedAt)
		fresh := ho.Fresh(time.Duration(set.HandoffMaxAge)*time.Second, now)
		if newer && (r.reentered || fresh) {
			return false // the handoff is written: the session is exiting
		}
	}
	if r.reentered || r.countAll {
		if d.State.HandoffBlocks >= set.HandoffMaxBlks {
			d.State.HandoffFailed = true
			d.Persist = true
			return false
		}
		d.State.HandoffBlocks++
	}
	d.State.BlockedAt = stamp.Format(now)
	d.Persist = true
	d.Block = true
	return true
}

// mergeHandoffBlock carries the block counters holdForHandoff writes onto cur.
func mergeHandoffBlock(cur, decided State) State {
	cur.HandoffBlocks, cur.BlockedAt, cur.HandoffFailed = decided.HandoffBlocks, decided.BlockedAt, decided.HandoffFailed
	return cur
}

// mergeStop is DecideStop's merge: the block counters plus the usage handoff.
func mergeStop(cur, decided State) State {
	cur = mergeHandoffBlock(cur, decided)
	cur.UsageHandoff = decided.UsageHandoff
	return cur
}

func blockReason(pct float64, threshold int, path string) string {
	return fmt.Sprintf("Context is at %s%%, at or above the handoff threshold of %d%%. "+
		"Write the handoff for the next orchestrator session to %s now: first line %q, "+
		"second line \"<!-- written <UTC timestamp, YYYY-MM-DDTHH:MM:SSZ> -->\", then the body from "+
		"references/handoff-template.md with a \"## Round state\" section (slots, who holds what, merges pending, escalations open). "+
		"Commit nothing you do not own. Then run /exit.",
		trimPct(pct), threshold, path, HandoffMarker)
}

func trimPct(p float64) string {
	s := fmt.Sprintf("%.1f", p)
	return strings.TrimSuffix(s, ".0")
}

// StopOutput is the JSON the hook prints for a block.
func StopOutput(reason string) map[string]string {
	return map[string]string{"decision": "block", "reason": reason}
}
