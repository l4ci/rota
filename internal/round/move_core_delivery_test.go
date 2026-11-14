package round

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/worker"
)

// #3001: the unresolved-delivery refusal points at `round transfer` as the way
// to hand the issue to a free slot without the maintainer. That only holds if
// transfer works from a slot whose Core delivery is unknown, so pin it: the
// sender is parked, the stuck record stays on it and the receiver takes the issue.
func TestTransferWorksFromASlotWithAnUnknownCoreDelivery(t *testing.T) {
	f := newMoveFx(t)
	if _, err := f.assign("12", "ben"); err != nil {
		t.Fatal(err)
	}
	if err := worker.Update(f.root, func(d *worker.Doc) {
		p := jsonx.NewObject()
		p.Set("state", "unknown")
		d.Slot("ben").Raw().Set("coreDelivery", p)
	}); err != nil {
		t.Fatal(err)
	}
	res, err := f.transfer("12", "dana", nil)
	if err != nil {
		t.Fatalf("transfer from a slot with an unknown Core delivery: %v", err)
	}
	if res.To != "dana" {
		t.Fatalf("transferred to %q, want dana", res.To)
	}
	if got := f.slot("dana").Task(); got != "12" {
		t.Errorf("receiver dana holds task %q, want 12", got)
	}
	if f.slot("ben").Task() != "" {
		t.Errorf("sender ben still holds the issue")
	}
	// The delivery stays unresolved: the slot is parked, not unblocked.
	if v, _ := f.slot("ben").Raw().Get("coreDelivery"); v == nil {
		t.Errorf("transfer erased ben's unknown delivery; only --confirm-not-submitted may clear it")
	}
}

// coreHost advertises Core's delivery receipts on the shared host fake.
type coreHost struct{ *hostFake }

type noConfirm struct{}

func (noConfirm) ConfirmNotSubmitted(context.Context, string, string) error { return nil }

func (h coreHost) Capabilities() host.Capabilities {
	c := h.hostFake.Capabilities()
	c.DeliveryReconciler = noConfirm{}
	return c
}

// #3001: `round assign` onto a slot with an unknown Core delivery refuses at
// stage dispatch (blockedBy dispatch) and its message carries the transfer hint
// next to the maintainer-confirmed --confirm-not-submitted path.
// #3320: assign always starts a fresh session, so a Core delivery saved for the
// slot's earlier session no longer refuses it (it used to, naming transfer).
func TestAssignFreshSessionIgnoresAnUnknownCoreDelivery(t *testing.T) {
	f := newAssignFixture(t)
	f.wire(func(w *worker.Env) { w.NewHost = func(host.Kind) host.Host { return coreHost{f.host} } })
	if err := worker.Update(f.root, func(d *worker.Doc) {
		p := jsonx.NewObject()
		p.Set("state", "unknown")
		p.Set("request", "an-earlier-message")
		p.Set("payload", "earlier")
		s := d.Slot("ben")
		s.Raw().Set("coreDelivery", p)
		s.Raw().Set("handle", "t78")
	}); err != nil {
		t.Fatal(err)
	}
	res, err := f.assign("12", "ben", nil)
	if err != nil {
		t.Fatalf("an earlier delivery blocked a fresh session: %v", err)
	}
	if !res.Dispatched || len(f.host.spawned) == 0 {
		t.Fatalf("assign did not start a fresh session: %+v", res)
	}
	s := worker.LoadRegistryTolerant(f.root).Slot("ben")
	if s.Task() != "12" || s.State() != "busy" || s.CoreDeliveryState() == "unknown" {
		t.Errorf("slot after a fresh assign: task %q state %q delivery %q", s.Task(), s.State(), s.CoreDeliveryState())
	}
}

// #3320: a transfer frees the sender; a delivery its worker demonstrably
// consumed goes with the work, one with no evidence stays (the pane may still
// hold the message).
func TestTransferClearsAConsumedCoreDeliveryOnly(t *testing.T) {
	for _, consumed := range []bool{true, false} {
		f := newMoveFx(t)
		if _, err := f.assign("12", "ben"); err != nil {
			t.Fatal(err)
		}
		saveCoreDelivery(t, f.root, "ben", "submitted")
		if err := rawSlot(f.root, "ben", func(s *jsonx.Object) {
			d, _ := s.Get("coreDelivery")
			d.(*jsonx.Object).Set("at", "2026-01-01T00:00:00Z")
		}); err != nil {
			t.Fatal(err)
		}
		if consumed {
			if _, err := worker.UpdateSlot(f.root, "ben", func(s *worker.Slot) { s.SetEvent(worker.EventDone, "", "2026-02-01T00:00:00Z") }); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := f.transfer("12", HumanTarget, nil); err != nil {
			t.Fatal(err)
		}
		got := worker.LoadRegistryTolerant(f.root).Slot("ben").CoreDeliveryState()
		if consumed && got != "" || !consumed && got != "submitted" {
			t.Errorf("consumed=%v: freed sender's delivery is %q", consumed, got)
		}
	}
}
