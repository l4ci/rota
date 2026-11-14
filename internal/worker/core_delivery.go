package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/stamp"
)

// The slot retains the signed payload as well as the host's durable transport
// receipt. A CLI retry must not rotate the signing key, replace the terminal,
// or allocate another relay number before reconciling that original payload.
func coreRequest(t *dispatchTarget, o DispatchOpts) string {
	identity := t.slot.Attempt() + "\x00" + o.Task + "\x00" + string(t.brief)
	if t.relayPR != "" {
		identity += "\x00pr:" + t.relayPR
	}
	sum := sha256.Sum256([]byte(identity))
	key := hex.EncodeToString(sum[:])
	if o.Relay {
		key = "relay:" + key
	}
	return key
}
func corePending(s *Slot) *jsonx.Object {
	v, _ := s.o.Get("coreDelivery")
	p, _ := v.(*jsonx.Object)
	return p
}

// receipts: the host keeps delivery receipts (Core), so its ErrNotSubmitted is
// proof that nothing went out and its outcome is recorded on the dispatch.
func receipts(h host.Host) bool { return h.Capabilities().DeliveryReconciler != nil }

func coreOutcome(err error) string {
	switch {
	case err == nil:
		return "picked-up"
	case errors.Is(err, host.ErrQueued):
		return "queued"
	case errors.Is(err, host.ErrSubmitted):
		return "submitted"
	case errors.Is(err, host.ErrNotSubmitted), errors.Is(err, host.ErrDraftOnPrompt), errors.Is(err, host.ErrDialogOpen):
		return "not-submitted"
	default:
		return "unknown"
	}
}

// CoreConsumed says the slot's saved Core delivery was demonstrably acted on
// (#3320); false when it has none. See coreConsumed for the evidence.
func (e Env) CoreConsumed(ctx context.Context, root string, s *Slot) bool {
	p := corePending(s)
	return p != nil && e.coreConsumed(ctx, root, s, s.Worktree(), p)
}

// coreConsumed is the consumption evidence for a saved delivery: a done event
// stamped after it (worker done), or a commit on the slot's remote-tracking
// branch, past its base, dated after it. A ROTA-DONE line the poll records
// clears the delivery itself (recordRow). No pane text is read here, and an
// unpushed local commit proves nothing. A delivery without a stamp (saved by an
// older rota) has no evidence and stays blocked.
func (e Env) coreConsumed(ctx context.Context, root string, s *Slot, worktree string, p *jsonx.Object) bool {
	at, err := stamp.Parse(jsonx.Str(p, "at"))
	if err != nil {
		return false
	}
	if ev := s.Event(); ev != nil && ev.Kind == EventDone {
		if ts, err := stamp.Parse(ev.TS); err == nil && ts.After(at) {
			return true
		}
	}
	b, base := s.Branch(), s.Base()
	if b == "" || base == "" || worktree == "" {
		return false
	}
	remote, err := e.Remote(root)
	if err != nil || remote == "" {
		return false
	}
	out, code := e.runGit(ctx, worktree, "log", "-1", "--format=%cI", "refs/remotes/"+remote+"/"+b, "--not", base)
	if code != 0 {
		return false
	}
	ct, err := time.Parse(time.RFC3339, strings.TrimSpace(out))
	return err == nil && ct.After(at)
}

// coreFresh is the saved delivery's end when its session is gone or consumed:
// the record is dropped so the slot takes the next message.
func coreFresh(root, slot string) error {
	_, err := UpdateSlot(root, slot, func(s *Slot) { s.ClearCoreDelivery() })
	return err
}

// coreRetry reconciles a saved Core delivery before a new message goes out.
// fresh is a task dispatch: it kills the old session and starts a new one, so
// an earlier delivery to the dead session never blocks it (#3320).
func (e Env) coreRetry(ctx context.Context, root string, o DispatchOpts, t *dispatchTarget, res DispatchResult, fresh bool) (bool, DispatchResult, error) {
	if !receipts(t.h) || t.handle == "" {
		return false, res, nil
	}
	p := corePending(t.slot)
	if p == nil {
		return false, res, nil
	}
	// A task dispatch under another harness is a fresh session even when the
	// task and brief repeat: the saved payload went to the old harness (#3320).
	if fresh && o.Kind != "" && o.Kind != t.slot.Kind() {
		return false, res, nil
	}
	if jsonx.Str(p, "request") != coreRequest(t, o) {
		if fresh {
			return false, res, nil
		}
		if e.coreConsumed(ctx, root, t.slot, t.worktree, p) {
			if err := coreFresh(root, o.Slot); err != nil {
				return true, res, err
			}
			return false, res, nil
		}
		state := jsonx.Str(p, "state")
		// A message Core holds a receipt for (unknown, submitted) is
		// reconciled before a new one is refused (#2640): with a receipt the
		// host only re-reads its evidence and never pastes it again. prepared
		// may have no receipt, so it is never re-sent from here.
		if state == "unknown" || state == "submitted" {
			err := e.sendSigned(ctx, t, o.Slot, jsonx.Str(p, "payload"), false)
			if saveErr := setCoreOutcome(root, o.Slot, err); saveErr != nil {
				return true, res, saveErr
			}
			state = coreOutcome(err)
		}
		switch state {
		case "prepared", "unknown", "submitted":
			res.Handle, res.Delivery = t.handle, state
			message := "Core has an unresolved delivery (state: " + state + "); inspect the original terminal and retry that message to reconcile; to give the issue to another slot without waiting, run rota round transfer " + transferID(o) + " --to <free slot> (slot " + o.Slot + " stays blocked until the delivery is confirmed)"
			if state == "unknown" {
				message += "; before using rota worker dispatch " + o.Slot + " --confirm-not-submitted, the maintainer must confirm after reading the original terminal screen that nothing was typed"
			}
			return true, res, fail(exitcode.ExitUnavailable, message)
		}
		return false, res, nil
	}
	if o.Relay && jsonx.Str(p, "state") == "not-submitted" {
		// A definite refusal left no relay log or input. Resume the normal
		// relay pipeline so its original sequence is logged on submission.
		return false, res, nil
	}
	res.Handle, res.Kind = t.handle, t.slot.Kind()
	err := e.sendSigned(ctx, t, o.Slot, jsonx.Str(p, "payload"), false)
	if saveErr := setCoreOutcome(root, o.Slot, err); saveErr != nil {
		return true, res, saveErr
	}
	res, err = sendFailure(t.h, o.Slot, res, err)
	return true, res, err
}
func setCoreOutcome(root, slot string, err error) error {
	_, saveErr := UpdateSlot(root, slot, func(s *Slot) {
		if p := corePending(s); p != nil {
			p.Set("state", coreOutcome(err))
			if errors.Is(err, host.ErrNotSubmitted) {
				s.SetUnsent(true)
			} else if coreOutcome(err) != "not-submitted" {
				s.SetUnsent(false)
			}
		}
	})
	return saveErr
}
func (e Env) sendTracked(ctx context.Context, root string, o DispatchOpts, t *dispatchTarget, payload string, resubmit bool) error {
	if !receipts(t.h) {
		return e.sendSigned(ctx, t, o.Slot, payload, resubmit)
	}
	if _, err := UpdateSlot(root, o.Slot, func(s *Slot) {
		request := coreRequest(t, o)
		p := jsonx.NewObject()
		// A relay retry rebuilds its delivery record; retain the manual
		// confirmation for this same request instead of erasing its audit.
		if prior := corePending(s); prior != nil && jsonx.Str(prior, "request") == request && jsonx.Bool(prior, "confirmedByHand") {
			p.Set("confirmedByHand", true)
			p.Set("confirmedAt", jsonx.Str(prior, "confirmedAt"))
		}
		p.Set("request", request)
		if t.relayPR != "" {
			p.Set("relayPR", t.relayPR)
		}
		p.Set("payload", payload)
		p.Set("state", "prepared")
		p.Set("at", stamp.Format(e.Now()))
		s.o.Set("coreDelivery", p)
	}); err != nil {
		return err
	}
	err := e.sendSigned(ctx, t, o.Slot, payload, false)
	if saveErr := setCoreOutcome(root, o.Slot, err); saveErr != nil {
		return errors.Join(host.ErrDeliveryUnknown, host.ErrMaybeSubmitted, saveErr)
	}
	return err
}

// ConfirmCoreNotSubmitted reconciles only the saved unknown delivery. It sends
// nothing and keeps the session and signing key, so the next dispatch can retry.
func (e Env) ConfirmCoreNotSubmitted(ctx context.Context, root, slot string) error {
	e = e.WithDefaults()
	if err := SoloRefusal(root, "solo rounds have no Core deliveries"); err != nil {
		return err
	}
	h := e.NewHost(e.hostKind(root))
	r := h.Capabilities().DeliveryReconciler
	if r == nil {
		return fail(exitcode.ExitRefused, "delivery confirmation requires herdr-core")
	}
	if err := h.Require(); err != nil {
		return fail(exitcode.ExitUnavailable, err.Error())
	}
	var reconcileErr error
	err := Update(root, func(d *Doc) {
		s := d.Slot(slot)
		if s == nil {
			reconcileErr = fail(exitcode.ExitResolution, fmt.Sprintf("slot '%s' is not in the pool", slot))
			return
		}
		p := corePending(s)
		if s.IsExternal() || s.PaneHandle() == "" || p == nil {
			reconcileErr = fail(exitcode.ExitRefused, "slot has no unknown Core delivery to confirm")
			return
		}
		if state := jsonx.Str(p, "state"); state != "unknown" {
			reconcileErr = fail(exitcode.ExitRefused, "slot '"+slot+"' has a Core delivery in state: "+state+"; retry the original dispatch to reconcile; to give the issue to another slot without waiting, run rota round transfer "+transferID(DispatchOpts{Task: s.HeldID()})+" --to <free slot>")
			return
		}
		payload := jsonx.Str(p, "payload")
		if payload == "" {
			reconcileErr = fail(exitcode.ExitRefused, "saved Core delivery has no payload")
			return
		}
		// An uncertain relay was provisionally logged. Retract just that last
		// entry, preserving older approvals and its sequence for the retry.
		var relayLog *jsonx.Object
		var relays []any
		if strings.HasPrefix(jsonx.Str(p, "request"), "relay:") {
			seq, _ := strconv.Atoi(harness.RelaySequence(strings.SplitN(payload, "\n", 2)[0]))
			relayLog = s.o
			if pr := jsonx.Str(p, "relayPR"); pr != "" {
				relayLog = d.queuedByRef(pr)
			} else if s.HeldID() == "" && s.PR() == "" {
				for _, q := range d.queued() {
					if jsonx.Str(q, "from") == slot {
						relayLog = q
					}
				}
			}
			if relayLog == nil {
				reconcileErr = fail(exitcode.ExitRefused, "unknown relay's PR is no longer on the review list; retain its evidence")
				return
			}
			relays = listOf(relayLog, "relays")
			if seq <= 0 || seq != len(relays) {
				reconcileErr = fail(exitcode.ExitRefused, "unknown relay does not match the last logged relay; retain its evidence")
				return
			}
		}
		if err := r.ConfirmNotSubmitted(ctx, s.PaneHandle(), payload); err != nil {
			reconcileErr = fail(exitcode.ExitRefused, err.Error())
			return
		}
		if relayLog != nil {
			relayLog.Set("relays", relays[:len(relays)-1])
		}
		p.Set("state", "not-submitted")
		p.Set("confirmedByHand", true)
		p.Set("confirmedAt", stamp.Format(e.Now()))
		s.SetUnsent(true)
		s.ClearRelayPending()
	})
	if err != nil {
		return err
	}
	return reconcileErr
}

// transferID is the issue the hint tells the orchestrator to move; the brief's
// task when known, else a placeholder.
func transferID(o DispatchOpts) string {
	if o.Task != "" {
		return o.Task
	}
	return "<id>"
}
