package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/l4ci/rota/internal/exitcode"
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
		switch jsonx.Str(p, "state") {
		case "prepared", "unknown", "submitted":
			res.Handle, res.Delivery = t.handle, jsonx.Str(p, "state")
			return true, res, fail(exitcode.ExitUnavailable, "Core has an unresolved delivery; inspect the original terminal and reconcile that message before sending another")
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
