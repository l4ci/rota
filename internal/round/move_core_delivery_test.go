package round

import (
	"context"
	"strings"
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
func TestAssignDispatchRefusalNamesTransferForAnUnknownCoreDelivery(t *testing.T) {
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
	f.host.sendErr = host.ErrSubmitted
	_, err := f.assign("12", "ben", nil)
	if err == nil {
		t.Fatal("assign onto a slot with an unknown delivery must refuse")
	}
	if se := stageOf(t, err); se.Stage != "dispatch" {
		t.Fatalf("want stage dispatch: %+v", se)
	}
	for _, want := range []string{"unresolved delivery", "rota round transfer <id> --to <free slot>", "stays blocked until the delivery is confirmed", "--confirm-not-submitted", "the maintainer must confirm after reading the original terminal screen"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("assign error lacks %q: %v", want, err)
		}
	}
}
