package round

import (
	"context"
	"fmt"
	"slices"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

// Slot selection has one owner. A roster slot takes the next issue in this
// order: the slot already holding it (resume), an idle slot, then a slot whose
// PR can be queued. Assign, best-of, the architecture and quota counts and the
// limit watcher all read it from here.

// SlotIdle says whether a roster slot can take an issue now: it holds nothing
// and is not busy (a live session is working in its pane). An adopted
// (external) slot has no session to dispatch into, so Transfer and assign
// refuse it; it is never idle.
func SlotIdle(s *worker.Slot) bool {
	return s != nil && s.HeldID() == "" && s.State() != "busy" && !s.IsExternal()
}

// SlotAccount is the harness kind ("codex", or "" for Claude) and the account
// a slot last ran under.
func SlotAccount(s *worker.Slot) (kind, account string) {
	if h := harness.Resolve(s.Kind()); !h.UsageMeter() {
		return h.Kind(), s.HomeAccount()
	}
	return "", s.Account()
}

// SlotLoginProblem says why a slot's account cannot start a session: its
// config dir has no usable Claude login (Env.Login). "" when it
// has one, and for a slot with no config dir or on a harness without a usage
// meter (codex), which this check does not cover.
func (e Env) SlotLoginProblem(s *worker.Slot) string {
	if s == nil || s.ConfigDir() == "" || !harness.Resolve(s.Kind()).UsageMeter() {
		return ""
	}
	return e.Login.Problem(worker.ExpandConfigDir(s.ConfigDir()))
}

// loginHint names the two commands that repair a slot whose account has no login.
func loginHint(s *worker.Slot) string {
	return "rota worker account assign " + s.Name() + " <account> moves it to another account, or CLAUDE_CONFIG_DIR=" + s.ConfigDir() + " claude /login logs this one in again"
}

// Members splits round.roster into the round's slots. Active is the first n
// names, where n is the count `round start` recorded (--slots, else
// round.slots), capped at the roster: only they take new work. Draining is
// the roster slots past n that still hold work (HoldsWork): they stay in the
// round, take nothing new, and leave it once their work lands or moves.
// Other registered slots past n are parked outside the round (#1097 D3).
// It reads, never writes: only start persists the count.
func Members(reg worker.Registry, set roundcfg.Settings) (active, draining []string) {
	n := reg.SlotCount()
	if n <= 0 {
		n = set.Slots
	}
	if n <= 0 || n > len(set.Roster) {
		n = len(set.Roster)
	}
	for _, name := range set.Roster[n:] {
		if s := reg.Slot(name); s != nil && HoldsWork(reg, s) {
			draining = append(draining, name)
		}
	}
	return set.Roster[:n], draining
}

// HoldsWork says whether a slot still has work in the round: an item, a
// claim, or a PR queued for review from it.
func HoldsWork(reg worker.Registry, s *worker.Slot) bool {
	if s.HeldID() != "" || s.ClaimID() != "" {
		return true
	}
	for _, q := range reg.PRs() {
		if q.From == s.Name() {
			return true
		}
	}
	return false
}

// CountIdle is how many roster slots are idle.
func CountIdle(reg worker.Registry, roster []string) int {
	n := 0
	for _, name := range roster {
		if SlotIdle(reg.Slot(name)) {
			n++
		}
	}
	return n
}

// IdleSlot is the first idle roster slot on the given harness kind and
// account, which is the receiver the limit watcher moves a limited slot's
// issue to. Transfer accepts every slot it returns.
func IdleSlot(reg worker.Registry, roster []string, kind, account string) (string, bool) {
	for _, name := range roster {
		s := reg.Slot(name)
		if !SlotIdle(s) {
			continue
		}
		if k, a := SlotAccount(s); k == kind && a == account {
			return name, true
		}
	}
	return "", false
}

// pickSlots chooses up to n roster slots for the issue held (a normalised ID):
// the pre-chosen slots first, then holders of the issue (resume, any roster
// slot, a draining one included), then the round's active slots that are
// idle or whose PR can be queued: only they take new work (Members). A slot
// is never listed twice. A new-work slot whose account has no login
// (Env.SlotLoginProblem) is not picked: skipped names each with its problem, for
// the caller's warning.
func (e Env) pickSlots(ctx context.Context, reg worker.Registry, set roundcfg.Settings, held string, n int, pre ...*worker.Slot) (out []*worker.Slot, skipped []string) {
	roster := set.Roster
	active, _ := Members(reg, set)
	add := func(s *worker.Slot) {
		if s != nil && len(out) < n && !slices.ContainsFunc(out, func(p *worker.Slot) bool { return p.Name() == s.Name() }) {
			out = append(out, s)
		}
	}
	for _, s := range pre {
		add(s)
	}
	for _, name := range roster {
		if s := reg.Slot(name); s != nil && s.HeldID() == held {
			add(s)
		}
	}
	usable := func(s *worker.Slot) bool {
		if why := e.SlotLoginProblem(s); why != "" {
			skipped = append(skipped, fmt.Sprintf("%s (%s in %s)", s.Name(), why, s.ConfigDir()))
			return false
		}
		return true
	}
	for _, name := range active {
		if s := reg.Slot(name); SlotIdle(s) && len(out) < n && usable(s) {
			add(s)
		}
	}
	for _, name := range active {
		if len(out) == n {
			break
		}
		if s := reg.Slot(name); s != nil {
			if ok, _ := e.parkable(ctx, s); ok && usable(s) {
				add(s)
			}
		}
	}
	return out, skipped
}

// checkActive refuses new work for a roster slot past the round's slot count:
// it drains, or is parked outside the round. resuming is a slot taking back
// the issue it already holds, which is not new work.
func checkActive(reg worker.Registry, set roundcfg.Settings, name string, resuming bool) error {
	active, draining := Members(reg, set)
	if resuming || slices.Contains(active, name) {
		return nil
	}
	why := "is parked outside the round"
	if slices.Contains(draining, name) {
		why = "is draining: it finishes the work it holds"
	}
	return blocked(BlockDraining, "slot %s %s and takes no new work; the round uses its first %d roster slots (round.slots or --slots)", name, why, len(active))
}
