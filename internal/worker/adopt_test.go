package worker

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
)

func TestDispatchRefusesExternal(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	if err := RegisterExternal(dir, "ext-1", "codex/12-x", "", "main", "12", ""); err != nil {
		t.Fatal(err)
	}
	s := LoadRegistry(dir).Slot("ext-1")
	if !s.IsExternal() || s.Handle() != "" || s.Task() != "12" || s.Kind() != KindExternal {
		t.Fatalf("slot: %v", s.Raw())
	}
	for _, relay := range []bool{false, true} {
		f := tmuxFake()
		_, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "ext-1", BodyFile: writeBrief(t, "go\n"), Task: "#12", Relay: relay})
		var xe *exitcode.Error
		if !errors.As(err, &xe) || xe.Exit != exitcode.ExitRefused || xe.Message != "external slot has no host" {
			t.Fatalf("relay=%v: %v", relay, err)
		}
		if bd, ok := xe.Data.(BlockData); !ok || bd.BlockedBy != "host" {
			t.Errorf("relay=%v data: %#v", relay, xe.Data)
		}
	}
}

func TestPollSkipsExternal(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	if err := RegisterExternal(dir, "ext-1", "codex/12-x", "", "main", "12", ""); err != nil {
		t.Fatal(err)
	}
	f.panes["w1"] = []string{"a\n", "a\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	f.panes["ext-1"] = []string{"ROTA-DONE ext-1 https://x/pull/1\n", "ROTA-DONE ext-1 https://x/pull/1\n"}
	before := slotField(t, dir, "ext-1", "state")
	res, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Slots {
		if r.Name == "ext-1" {
			t.Errorf("external slot classified: %+v", r)
		}
	}
	for _, c := range f.calls {
		if c == "capture ext-1" {
			t.Errorf("pane read for an external slot: %v", f.calls)
		}
	}
	if got := slotField(t, dir, "ext-1", "state"); got != before {
		t.Errorf("state %s -> %s", before, got)
	}
	// Naming it polls nothing.
	res, err = envWith(f).Poll(bg, dir, PollOpts{Slot: "ext-1", Lines: 60})
	if err != nil || len(res.Slots) != 0 {
		t.Errorf("named external: %+v %v", res, err)
	}
}

func TestHarnessKindHidesExternal(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	if err := RegisterExternal(dir, "ext-1", "codex/12-x", "", "main", "12", ""); err != nil {
		t.Fatal(err)
	}
	if s := LoadRegistry(dir).Slot("ext-1"); s.HarnessKind() != "" || !s.IsExternal() {
		t.Errorf("kind %q", s.HarnessKind())
	}
}

func TestSoloWaitIgnoresExternal(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	if err := RegisterExternal(dir, "ext-1", "codex/12-x", "", "main", "12", ""); err != nil {
		t.Fatal(err)
	}
	if err := UpdateSlots(dir, func(s *Slot) {
		if s.IsExternal() {
			_ = s.MarkState("done", "2026-10-02T15:04:05Z")
		}
	}); err != nil {
		t.Fatal(err)
	}
	res, err := soloWait(dir, WaitOpts{})
	if err == nil {
		t.Errorf("solo wait watched the external slot: %+v", res)
	}
}

func TestRegisterExternalChecksUniquenessUnderTheLock(t *testing.T) {
	dir := newProject(t, `{}`)
	if err := RegisterExternal(dir, "ext-1", "codex/12-x", "", "main", "12", ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, branch, task, kind string }{
		{"ext-1", "codex/13-y", "13", ConflictName},
		{"ext-2", "codex/12-x", "13", ConflictBranch},
		{"ext-2", "codex/14-z", "12", ConflictIssue},
	} {
		err := RegisterExternal(dir, c.name, c.branch, "", "main", c.task, "")
		var rc *RegisterConflict
		if !errors.As(err, &rc) || rc.Kind != c.kind || rc.Slot != "ext-1" {
			t.Errorf("%+v: %v", c, err)
		}
	}
	if n := len(LoadRegistry(dir).Slots()); n != 1 {
		t.Errorf("%d slots after refused registrations", n)
	}
}
