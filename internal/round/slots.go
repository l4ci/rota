package round

import (
	"context"
	"slices"

	"github.com/l4ci/rota/internal/harness"
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
// the pre-chosen slots first, then holders of the issue (resume), idle slots,
// and slots whose PR can be queued. A slot is never listed twice.
func (e Env) pickSlots(ctx context.Context, reg worker.Registry, roster []string, held string, n int, pre ...*worker.Slot) []*worker.Slot {
	var out []*worker.Slot
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
	for _, name := range roster {
		if s := reg.Slot(name); SlotIdle(s) {
			add(s)
		}
	}
	for _, name := range roster {
		if len(out) == n {
			break
		}
		if s := reg.Slot(name); s != nil {
			if ok, _ := e.parkable(ctx, s); ok {
				add(s)
			}
		}
	}
	return out
}
