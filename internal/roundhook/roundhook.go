// Package roundhook is the orchestrator-facing view of a round that the Stop
// and prompt hooks read: the one-line digest, the exit digest and the Stop
// block. The watcher loop lives in roundwatch and its marker beside the lease; this package
// reads that marker (roundlease.Armed) and the registry, nothing else of the
// loop.
package roundhook

import (
	"errors"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/landfreeze"
	"github.com/l4ci/rota/internal/rotastate"
	"github.com/l4ci/rota/internal/roundlease"
	"github.com/l4ci/rota/internal/worker"
)

// Digest is the one-line round state the prompt hook shows: each active
// slot with its issue and PR, the open escalations and whether a watch is armed.
func Digest(we worker.Env, root string, round int, armed bool) string {
	line := roundState(we, root, round)
	if armed {
		return line + ". Watch armed."
	}
	return line + ". NO WATCH ARMED: run `rota round watch` in the background now."
}

// ExitDigest describes the round as its watch returns and must be re-armed.
func ExitDigest(we worker.Env, root string, round int) string {
	return roundState(we, root, round) + ". Watch exiting; re-arm `rota round watch`."
}

// activeSlot is a slot the round is still waiting on.
type activeSlot struct {
	slot  *worker.Slot
	state string
	// wake: the slot needs the orchestrator (a done slot whose PR is not merged).
	wake bool
}

// roundView is the one walk over the registry that the digest and the Stop
// hook both read: the slots that are not idle and the escalations still open.
type roundView struct {
	active []activeSlot
	open   int
}

func viewOf(reg worker.Registry) roundView {
	var v roundView
	for _, s := range reg.Slots() {
		st := strings.ToLower(s.State())
		if worker.IsIdle(st) {
			continue
		}
		v.active = append(v.active, activeSlot{slot: s, state: st, wake: worker.NeedsWake(st) && !s.PRMerged()})
	}
	v.open = len(escalation.Open(escalation.FromRegistry(reg)))
	return v
}

// needsWatch: anything a watch would wake the orchestrator for, and the slots
// already waiting on it.
func (v roundView) needsWatch() (bool, []string) {
	var attn []string
	for _, a := range v.active {
		if a.wake {
			attn = append(attn, a.slot.Name()+" "+a.state)
		}
	}
	return len(v.active) > 0 || v.open > 0, attn
}

func roundState(we worker.Env, root string, round int) string {
	we = we.WithDefaults()
	ctx, run := we.Ctx, we.Git
	var parts []string
	if frozen, err := landfreeze.Load(ctx, run, root); err != nil {
		if !errors.Is(err, rotastate.ErrNotRepo) {
			parts = append(parts, landfreeze.UnreadableLine(err))
		}
	} else if frozen != nil && frozen.Active {
		parts = append(parts, frozen.Line())
	}
	// Tolerant: a hook digest cannot refuse; a corrupt registry reads as no slots.
	v := viewOf(we.Registry(root))
	for _, a := range v.active {
		s, st := a.slot, a.state
		p := s.Name() + " " + st
		if lv := worker.JudgeLiveness(worker.LivenessEvidence{State: st, Reason: s.Reason()}); lv.State == worker.LivenessUnverifiable && s.Reason() != "" {
			p += ":" + s.Reason()
		}
		is := s.Issue()
		if is == "" {
			is = s.HeldID()
		}
		if is != "" {
			p += " #" + strings.TrimPrefix(is, "#")
		}
		if pr := s.PR(); pr != "" {
			p += " " + pr
		}
		if a.wake {
			p += " (needs you)"
		}
		parts = append(parts, p)
	}
	if v.open > 0 {
		parts = append(parts, fmt.Sprintf("%d escalation(s) pending", v.open))
	}
	line := "rota round"
	if round > 0 {
		line += fmt.Sprintf(" %d", round)
	}
	if len(parts) == 0 {
		line += ": no active slots"
	} else {
		line += ": " + strings.Join(parts, "; ")
	}
	return line
}

// NeedsWatch is the Stop hook's question: is there anything a watch would
// wake the orchestrator for? A slot that is not idle, or an escalation still
// pending. An all-idle round has nothing to wait on, so the orchestrator may
// stop (it has candidates to assign, not events to watch).
func NeedsWatch(we worker.Env, root string) bool {
	need, _ := viewOf(we.WithDefaults().Registry(root)).needsWatch()
	return need
}

// StopBlock refuses an idle orchestrator with active workers and no live
// watch: nothing would wake it when a worker finishes. Solo rounds have no
// panes to watch. The caller has established that this is the orchestrator
// and that this is the first stop in the turn, so a refused watch cannot loop.
func StopBlock(we worker.Env, env roundlease.Env, root, commonDir string) (reason string, block bool) {
	reg := we.WithDefaults().Registry(root)
	if worker.IsSoloHost(reg.Host()) {
		return "", false
	}
	need, attn := viewOf(reg).needsWatch()
	if !need {
		return "", false
	}
	if _, armed := roundlease.Armed(env, commonDir); armed {
		return "", false
	}
	reason = "No `rota round watch` is running, and workers are active: nothing would wake you when one finishes. " +
		"Start `rota round watch` as a background command now (one at a time; it exits with JSON when a slot, PR or escalation changes, or at its heartbeat), then stop."
	if len(attn) > 0 {
		reason += " Waiting on you already: " + strings.Join(attn, ", ") + "."
	}
	return reason, true
}
