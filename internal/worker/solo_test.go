package worker

import (
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/host"
)

// recordHost marks the project's registry as a round on the given host.
func recordHost(t *testing.T, dir string, h host.Kind) {
	t.Helper()
	if err := Update(dir, func(d *Doc) { d.SetHost(h) }); err != nil {
		t.Fatal(err)
	}
}

func setState(t *testing.T, dir, slot, state string) {
	t.Helper()
	if _, err := UpdateSlot(dir, slot, func(s *Slot) { s.Raw().Set("state", state) }); err != nil {
		t.Fatal(err)
	}
}

// soloEnv is an Env whose host constructor fails the test: under solo no verb
// may build a host, so none may drive one.
func soloEnv(t *testing.T) Env {
	e := envWith(nil)
	e.NewHost = func(d host.Kind) host.Host {
		t.Fatalf("a solo round reached host.New(%q)", d)
		return nil
	}
	return e
}

func soloProject(t *testing.T) string {
	t.Helper()
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 2, Base: "main"})
	recordHost(t, dir, "solo")
	return dir
}

func requireSoloRefusal(t *testing.T, what string, err error, hint string) {
	t.Helper()
	we, ok := err.(*exitcode.Error)
	if !ok || we.Exit != exitcode.ExitUsage || we.Message != "solo round: workers are subagents, there are no panes" {
		t.Fatalf("%s: err = %v, want exit 2 with the solo message", what, err)
	}
	if !strings.Contains(we.Hint, hint) {
		t.Errorf("%s: hint %q does not name %q", what, we.Hint, hint)
	}
}

func TestPaneVerbsRefuseUnderSolo(t *testing.T) {
	dir := soloProject(t)
	e := soloEnv(t)
	body := writeBrief(t, "go\n")

	_, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: body, Task: "12"})
	requireSoloRefusal(t, "dispatch", err, "round assign")
	_, err = e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: body, Relay: true})
	requireSoloRefusal(t, "dispatch --relay", err, "round report")
	_, err = e.Poll(bg, dir, PollOpts{})
	requireSoloRefusal(t, "poll", err, "round report")
	_, err = e.SessionEnsure(bg, dir, SessionOpts{})
	requireSoloRefusal(t, "session ensure", err, "no host session")
	if err := SoloRefusal(dir, "x"); err == nil {
		t.Error("SoloRefusal (session check, account pick/assign) must refuse under solo")
	}
	// Closing a pane is skipped, not refused: a subagent has none.
	if err := e.KillSlot(bg, dir, "w1"); err != nil {
		t.Errorf("KillSlot under solo: %v", err)
	}
}

func TestSoloRefusalIsNilWithoutASoloHost(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	for _, h := range []host.Kind{"", host.Tmux, host.Herdr} {
		if h != "" {
			recordHost(t, dir, h)
		}
		if err := SoloRefusal(dir, "x"); err != nil {
			t.Errorf("host %q: %v", h, err)
		}
	}
	p := RegistryPath(dir)
	if err := os.WriteFile(p, []byte(`{"slots": [{"name": "ben"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsSolo(dir) {
		t.Error("a corrupt registry must read as not solo (tolerant load)")
	}
}

func TestRecordedHostOverridesWorkDispatchAndNoneKeepsIt(t *testing.T) {
	var got []host.Kind
	e := envWith(tmuxFake())
	e.NewHost = func(d host.Kind) host.Host {
		got = append(got, d)
		return tmuxFake()
	}
	dir := newProject(t, `{"round":{"dispatch":"tmux"}}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	e.SessionCheck(bg, dir)
	recordHost(t, dir, "herdr")
	e.SessionCheck(bg, dir)
	if len(got) != 2 || got[0] != "tmux" || got[1] != "herdr" {
		t.Errorf("host kinds = %v, want [tmux herdr]: no recorded host keeps round.dispatch, a recorded one wins", got)
	}
}

func TestWaitUnderSoloNeverBlocks(t *testing.T) {
	dir := soloProject(t)
	e := soloEnv(t)

	// Every slot idle: nothing is watched.
	if _, err := e.Wait(bg, dir, WaitOpts{}); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("all idle: %v", err)
	}
	// A busy slot has no handle and is still watched; it has not reported.
	setState(t, dir, "w1", "busy")
	res, err := e.Wait(bg, dir, WaitOpts{})
	if err != nil || !res.TimedOut || res.Waited != 0 || len(res.Slots) != 1 || res.Slots[0].Name != "w1" || res.Slots[0].State != "busy" {
		t.Fatalf("busy: %+v %v", res, err)
	}
	// A report ends it at once, from the registry.
	setState(t, dir, "w1", "done")
	res, err = e.Wait(bg, dir, WaitOpts{})
	if err != nil || res.TimedOut || res.Slot != "w1" || res.State != "done" || res.Source != SourceRegistry {
		t.Fatalf("done: %+v %v", res, err)
	}
	// Named slots: unknown is 3; an idle named slot is returned, not skipped.
	if _, err := e.Wait(bg, dir, WaitOpts{Slots: []string{"nope"}}); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("unknown slot: %v", err)
	}
	res, err = e.Wait(bg, dir, WaitOpts{Slots: []string{"w2"}})
	if err != nil || res.Slot != "w2" || res.State != "idle" {
		t.Errorf("named idle: %+v %v", res, err)
	}
	// The first non-busy slot in registry order wins.
	setState(t, dir, "w1", "busy")
	setState(t, dir, "w2", "blocked")
	res, _ = e.Wait(bg, dir, WaitOpts{})
	if res.Slot != "w2" || res.State != "blocked" {
		t.Errorf("%+v", res)
	}
}

// A registry wake carries the done event's body, so `round watch` shows the
// outcome and its summary beside the slot state.
func TestWaitCarriesTheOutcomeEvidence(t *testing.T) {
	dir := soloProject(t)
	e := soloEnv(t)
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) {
		s.SetOutcome(OutcomePartial, []string{"a", "b", "c"}, "2026-10-20T00:00:00Z")
		s.SetEvent(EventDone, "partial: a | b | c", "2026-10-20T00:00:00Z")
		s.MarkState("done", "2026-10-20T00:00:00Z")
	}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Wait(bg, dir, WaitOpts{})
	if err != nil || res.Slot != "w1" || res.Evidence != "partial: a | b | c" {
		t.Fatalf("wait = %+v (%v), want the outcome as evidence", res, err)
	}
}

func TestIsSoloReadsTheRecordedHost(t *testing.T) {
	dir := newProject(t, `{}`)
	if IsSolo(dir) {
		t.Error("no registry must not read as solo")
	}
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	for _, c := range []struct {
		h    host.Kind
		want bool
	}{{host.Solo, true}, {host.Tmux, false}, {host.Herdr, false}} {
		recordHost(t, dir, c.h)
		if got := IsSolo(dir); got != c.want {
			t.Errorf("host %q: IsSolo = %v, want %v", c.h, got, c.want)
		}
	}
}
