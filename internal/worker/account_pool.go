package worker

import (
	"math"
	"time"

	"github.com/l4ci/rota/internal/harness"
)

// PoolEntry is one account of the worker pool: every round.accounts entry of
// an allowed harness kind, or the orchestrator's own login (Name "") for an
// allowed kind with none.
type PoolEntry struct {
	Kind, Name string
	Meter      Meter
	// Load is how many slots hold work on this account right now.
	Load int
	// Rank is the kind's position in round.harnesses (the tie-break order),
	// Order the entry's position in the pool.
	Rank, Order int
	// Skip says why the entry cannot take a session (no login, limited by the
	// watcher log); "" means usable unless its meter is cooling.
	Skip string
}

// Usable reports whether the entry can take a new session.
func (p PoolEntry) Usable() bool { return p.Skip == "" && p.Meter.Verdict != VerdictCooling }

// PoolRef names the account a slot runs under now.
type PoolRef struct{ Kind, Name string }

// PoolChoice is the account a slot goes to.
type PoolChoice struct {
	Entry PoolEntry
	// Kept: the slot's current account stays; Gap is how far the best usable
	// account is ahead of it, in headroom points; nil when either is unknown.
	Kept bool
	Gap  *float64
}

// ChoosePool picks the account for a slot. Known headroom beats unknown. A
// usable current account is kept unless the best account is ahead of it by at
// least margin points; an unknown reading on either side keeps it, and a
// margin of 0 never moves it. A slot that moves goes to the account with the
// fewest slots among those within margin of the best headroom (the spread
// that keeps one tick from sending every fresh slot to one account), then
// round.harnesses order, then pool order. ok is false when no entry is usable.
func ChoosePool(entries []PoolEntry, current *PoolRef, margin int) (PoolChoice, bool) {
	var usable []PoolEntry
	var best *float64
	for _, e := range entries {
		if !e.Usable() {
			continue
		}
		usable = append(usable, e)
		if h := e.Meter.Headroom; h != nil && (best == nil || *h > *best) {
			best = h
		}
	}
	if len(usable) == 0 {
		return PoolChoice{}, false
	}
	if current != nil {
		for _, e := range usable {
			if e.Kind != current.Kind || e.Name != current.Name {
				continue
			}
			c := PoolChoice{Entry: e, Kept: true}
			if best != nil && e.Meter.Headroom != nil {
				gap := *best - *e.Meter.Headroom
				c.Gap = &gap
				if margin > 0 && gap >= float64(margin) {
					c.Kept = false
				}
			}
			if c.Kept {
				return c, true
			}
			break
		}
	}
	var spread []PoolEntry
	for _, e := range usable {
		if best == nil || e.Meter.Headroom != nil && *e.Meter.Headroom >= *best-float64(max(margin, 0)) {
			spread = append(spread, e)
		}
	}
	pick := spread[0]
	for _, e := range spread[1:] {
		if e.Load < pick.Load || e.Load == pick.Load && (e.Rank < pick.Rank || e.Rank == pick.Rank && e.Order < pick.Order) {
			pick = e
		}
	}
	c := PoolChoice{Entry: pick}
	if current != nil {
		c.Kept = pick.Kind == current.Kind && pick.Name == current.Name
		for _, e := range usable {
			if e.Kind == current.Kind && e.Name == current.Name && e.Meter.Headroom != nil && best != nil {
				gap := *best - *e.Meter.Headroom
				c.Gap = &gap
			}
		}
	}
	return c, true
}

// QuotaMeter reads a display snapshot as a scheduling Meter on the same
// 0-100 scale as Claude's: headroom is 100 minus the highest used percent over
// every window the snapshot reports. No readable window (or an unavailable
// snapshot) is unknown, never 0. A window at 100% with a reset still ahead
// makes the account cooling.
func QuotaMeter(s harness.QuotaSnapshot, now time.Time) Meter {
	m := Meter{Name: s.Name, ConfigDir: s.Home, Verdict: VerdictUnknown, Reason: s.Reason}
	if s.Status == "unavailable" {
		return m
	}
	worst := -1.0
	for _, w := range s.Windows {
		// A stale window's reset already passed: its reading no longer says anything.
		if w.UsedPercent == nil || w.Status != "available" && w.Status != "partial" {
			continue
		}
		worst = math.Max(worst, *w.UsedPercent)
		if *w.UsedPercent >= 100 && w.ResetsAt != nil && w.ResetsAt.After(now) {
			m.Verdict = VerdictCooling
			if m.ResetsAt == nil || w.ResetsAt.After(*m.ResetsAt) {
				r := *w.ResetsAt
				m.ResetsAt = &r
			}
		}
	}
	if worst < 0 {
		return m
	}
	if m.Verdict != VerdictCooling {
		m.Verdict = VerdictFree
	}
	h := roundTenth(math.Max(0, 100-worst))
	m.Headroom = &h
	return m
}
