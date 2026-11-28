package worker

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// The land lock's holder file records what the running landing locked. This
// file is its only writer and reader format, and owns the rule that maps it to
// gated slots.

// landHolder is the holder file. No targets means nothing named a holder.
type landHolder struct {
	Targets []landTarget `json:"targets,omitempty"`
}

// landTarget is one thing a landing locked: exactly one field is set. Unscoped
// is anything that names no slot or PR (freeze recovery).
type landTarget struct {
	Slot     string `json:"slot,omitempty"`
	PR       string `json:"pr,omitempty"`
	Unscoped string `json:"unscoped,omitempty"`
}

// EncodeLandHolder is the holder file of a gate or train over targets, each a
// slot name or a PR ref.
func EncodeLandHolder(root string, targets ...string) []byte {
	var h landHolder
	reg := LoadRegistryTolerant(root)
	for _, t := range targets {
		t = strings.TrimSpace(t)
		switch _, isPR := PRRefNumber(t); {
		case t == "":
		case reg.Slot(t) != nil:
			h.Targets = append(h.Targets, landTarget{Slot: t})
		case isPR:
			h.Targets = append(h.Targets, landTarget{PR: t})
		default:
			h.Targets = append(h.Targets, landTarget{Unscoped: t})
		}
	}
	return h.encode()
}

// unscopedLandHolder is the holder file of a landing that locks no slot or PR.
func unscopedLandHolder(what string) []byte {
	return landHolder{Targets: []landTarget{{Unscoped: what}}}.encode()
}

func (h landHolder) encode() []byte {
	b, _ := json.Marshal(h) // strings and slices only: cannot fail
	return b
}

// parseLandHolder reads a holder file. Anything but the typed shape, such as
// the plain text an older rota wrote, is unscoped and keeps its text.
func parseLandHolder(b []byte) landHolder {
	var h landHolder
	if json.Unmarshal(b, &h) != nil {
		if text := strings.TrimSpace(string(b)); text != "" {
			return landHolder{Targets: []landTarget{{Unscoped: text}}}
		}
		return landHolder{}
	}
	return h
}

func (t landTarget) text() string {
	switch {
	case t.Slot != "":
		return t.Slot
	case t.PR != "":
		return t.PR
	}
	return t.Unscoped
}

// label names the holder's targets as recorded, rewritten by name.
func (h landHolder) label(name func(landTarget) string) string {
	var parts []string
	for _, t := range h.Targets {
		if s := name(t); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

// LandGate is what a running landing gates, as seen by a verb that would switch
// a slot worktree's branch.
type LandGate struct {
	Held bool // a gate or train holds the land lock
	// All: the holder is empty or names something that is neither a slot nor a
	// PR, so no slot is known to be safe.
	All    bool
	Slots  map[string]bool // the gated slots, when not All
	Holder string          // names the holder, slots by their recorded `#<PR>`; "" if unrecorded
}

// LandGate reports which slots a running landing gates: a named slot, and
// every slot or queued PR's origin slot whose PR number a target names, unless
// that origin slot has moved on to another branch. The zero value means the
// land lock is free.
func (e Env) LandGate(ctx context.Context, root string) (LandGate, error) {
	held, err := e.LandLockHeld(ctx, root)
	if err != nil || !held {
		return LandGate{}, err
	}
	h := e.landHolder(ctx, root)
	reg := LoadRegistryTolerant(root)
	g := LandGate{Held: true, All: len(h.Targets) == 0, Slots: map[string]bool{}}
	g.Holder = h.label(func(t landTarget) string {
		if s := reg.Slot(t.Slot); s != nil {
			if n, ok := PRRefNumber(s.PR()); ok {
				return "#" + strconv.Itoa(n)
			}
		}
		return t.text()
	})
	for _, t := range h.Targets {
		n, isPR := PRRefNumber(t.PR)
		switch {
		case t.Slot != "":
			g.Slots[t.Slot] = true
		case isPR:
			for _, s := range reg.Slots() {
				if m, ok := PRRefNumber(s.PR()); ok && m == n {
					g.Slots[s.Name()] = true
				}
			}
			for _, q := range reg.PRs() {
				if m, ok := PRRefNumber(q.PR); ok && m == n && q.From != "" && !movedOn(reg, q) {
					g.Slots[q.From] = true
				}
			}
		default:
			g.All = true
		}
	}
	if g.All {
		g.Slots = nil
	}
	return g, nil
}

// movedOn reports whether a queued PR's origin slot now holds a different
// branch. The gate merges the PR from its own record and scratch tree and the
// landing bookkeeping drops only that record, so the slot's worktree is not at
// risk, an idle slot included.
func movedOn(reg Registry, q QueuedPR) bool {
	s := reg.Slot(q.From)
	return s != nil && s.Branch() != q.Branch
}
