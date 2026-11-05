package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
)

// The slot retains the signed payload as well as the host's durable transport
// receipt. A CLI retry must not rotate the signing key, replace the terminal,
// or allocate another relay number before reconciling that original payload.
func coreRequest(t *dispatchTarget, o DispatchOpts) string {
	sum := sha256.Sum256([]byte(t.slot.Attempt() + "\x00" + o.Task + "\x00" + string(t.brief)))
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
func (e Env) coreRetry(ctx context.Context, root string, o DispatchOpts, t *dispatchTarget, res DispatchResult) (bool, DispatchResult, error) {
	if t.h.Kind() != host.HerdrCore || t.handle == "" {
		return false, res, nil
	}
	p := corePending(t.slot)
	if p == nil {
		return false, res, nil
	}
	if jsonx.Str(p, "request") != coreRequest(t, o) {
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
			res.Handle, res.Delivery = t.handle, jsonx.Str(p, "state")
			return true, res, fail(exitcode.ExitUnavailable, "Core has an unresolved delivery; inspect the original terminal and retry that message to reconcile; if nothing was typed, run rota worker dispatch "+o.Slot+" --confirm-not-submitted before sending another")
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
	if t.h.Kind() != host.HerdrCore {
		return e.sendSigned(ctx, t, o.Slot, payload, resubmit)
	}
	if _, err := UpdateSlot(root, o.Slot, func(s *Slot) {
		p := jsonx.NewObject()
		p.Set("request", coreRequest(t, o))
		p.Set("payload", payload)
		p.Set("state", "prepared")
		s.o.Set("coreDelivery", p)
	}); err != nil {
		return err
	}
	err := e.sendSigned(ctx, t, o.Slot, payload, false)
	if saveErr := setCoreOutcome(root, o.Slot, err); saveErr != nil {
		return errors.Join(host.ErrDeliveryUnknown, saveErr)
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
	if h.Kind() != host.HerdrCore || r == nil {
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
		if s.IsExternal() || s.PaneHandle() == "" || p == nil || jsonx.Str(p, "state") != "unknown" {
			reconcileErr = fail(exitcode.ExitRefused, "slot has no unknown Core delivery to confirm")
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
			if s.HeldID() == "" && s.PR() == "" {
				for _, q := range d.queued() {
					if jsonx.Str(q, "from") == slot {
						relayLog = q
					}
				}
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
		s.SetUnsent(true)
		s.ClearRelayPending()
	})
	if err != nil {
		return err
	}
	return reconcileErr
}
