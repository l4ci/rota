package worker

import (
	"fmt"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
)

func TestCorePoolStartsWithoutInventedTerminalHandle(t *testing.T) {
	dir := newProject(t, `{"round":{"dispatch":"herdr-core"}}`)
	if _, err := goInit(t, dir, InitOpts{Slots: 1, Base: "main"}); err != nil {
		t.Fatal(err)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").Handle(); got != "" {
		t.Fatalf("undispatched Core slot has invented handle %q", got)
	}
}

func TestCoreDispatchPreservesUncertainDelivery(t *testing.T) {
	h := &fakeHost{receipts: true, name: host.HerdrCore}
	_, err := sendFailure(h, "w1", DispatchResult{}, fmt.Errorf("%w: transport lost after possible submission", host.ErrMaybeSubmitted))
	if err == nil || !strings.Contains(err.Error(), "possible submission") || strings.Contains(err.Error(), "never picked up") {
		t.Fatalf("uncertain Core delivery was rewritten as safe-to-retry: %v", err)
	}
}

func TestCoreTaskRetryKeepsOriginalSession(t *testing.T) {
	dir := newProject(t, `{"round":{"dispatch":"herdr-core"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	h := &fakeHost{receipts: true, name: host.HerdrCore, sendErr: host.ErrDeliveryUnknown}
	rig := &codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}
	e := rig.env(h)
	o := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "work once"), Task: "T1", Kind: "codex"}
	_, err := e.Dispatch(bg, dir, o)
	if err == nil {
		t.Fatal("uncertain submission must be reported")
	}
	first := h.sent
	_, err = e.Dispatch(bg, dir, o)
	spawns := 0
	for _, c := range h.calls {
		if strings.HasPrefix(c, "spawn ") {
			spawns++
		}
	}
	if spawns != 1 {
		t.Fatalf("retry replaced possibly-working Core session: %d spawns", spawns)
	}
	if h.sent != first {
		t.Fatal("retry changed signed delivery identity")
	}
}

func TestCoreDefiniteRetryClearsUnsentAndLogsRelay(t *testing.T) {
	dir := newProject(t, `{"round":{"dispatch":"herdr-core"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	h := &fakeHost{receipts: true, name: host.HerdrCore, sendErr: host.ErrNotSubmitted}
	rig := &codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}
	e := rig.env(h)
	task := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "work once"), Task: "T1", Kind: "codex"}
	if _, err := e.Dispatch(bg, dir, task); err == nil {
		t.Fatal("initial refusal hidden")
	}
	h.sendErr = nil
	if _, err := e.Dispatch(bg, dir, task); err != nil {
		t.Fatal(err)
	}
	if LoadRegistryTolerant(dir).Slot("w1").Unsent() {
		t.Error("confirmed retry left task unsent")
	}
	relay := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "answer once"), Relay: true}
	h.sendErr = host.ErrDraftOnPrompt
	if _, err := e.Dispatch(bg, dir, relay); err == nil {
		t.Fatal("human draft ignored")
	}
	h.sendErr = nil
	if _, err := e.Dispatch(bg, dir, relay); err != nil {
		t.Fatal(err)
	}
	if s := LoadRegistryTolerant(dir).Slot("w1"); len(s.Relays()) != 1 || s.RelayPending() == 0 {
		t.Fatalf("safe relay retry lost provenance/pending marker: %+v", s.Raw())
	}
}

// #2640: a later message reconciles the unresolved one through the host first,
// so a pickup the host can now prove frees the slot; until then it refuses.
func TestCoreNewMessageReconcilesUnresolvedDelivery(t *testing.T) {
	dir := newProject(t, `{"round":{"dispatch":"herdr-core"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	h := &fakeHost{receipts: true, name: host.HerdrCore, sendErr: host.ErrSubmitted}
	rig := &codexRig{loggedIn: true, missing: map[string]bool{"herdr": true}}
	e := rig.env(h)
	task := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "work once"), Task: "T1", Kind: "codex"}
	if res, err := e.Dispatch(bg, dir, task); err != nil || res.Delivery != "submitted" {
		t.Fatalf("task: %+v %v, want submitted", res, err)
	}
	relay := DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "answer once"), Relay: true}
	if _, err := e.Dispatch(bg, dir, relay); err == nil || !strings.Contains(err.Error(), "unresolved delivery") || !strings.Contains(err.Error(), "the maintainer must confirm after reading the original terminal screen") {
		t.Fatalf("unconfirmed task must require the maintainer's screen confirmation: %v", err)
	}
	h.sendErr = nil // the host can now prove the task was picked up
	if _, err := e.Dispatch(bg, dir, relay); err != nil {
		t.Fatalf("relay after a proven pickup: %v", err)
	}
	if !strings.Contains(h.sent, "answer once") {
		t.Errorf("relay not delivered; last send %q", h.sent)
	}
}
