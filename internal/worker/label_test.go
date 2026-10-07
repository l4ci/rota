package worker

import (
	"context"
	"strings"
	"testing"
)

// labelHost adds host.Labeler to fakeHost and records every title set.
type labelHost struct {
	*fakeHost
	titles []string // "slot=title", "" title is a clear
	tokens []string
}

func (l *labelHost) Label(_ context.Context, slot, _ string, title string) {
	l.titles = append(l.titles, slot+"="+title)
}
func (l *labelHost) LabelWorkspace(_ context.Context, token string) {
	l.tokens = append(l.tokens, token)
}

func newLabelHost() *labelHost { return &labelHost{fakeHost: tmuxFake()} }

func TestSlotTitle(t *testing.T) {
	for _, c := range []struct{ slot, task, state, pr, want string }{
		{"nia", "#514", StateBusy, "", "nia · #514 · working"},
		{"nia", "#514", StateIdle, "", "nia · #514 · idle"},
		{"nia", "#514", StateNeedsPermission, "", "nia · #514 · needs permission"},
		{"nia", "#514", StateBlocked, "", "nia · #514 · blocked"},
		{"nia", "#514", StateDone, "", "nia · #514 · done"},
		{"nia", "#514", StateDone, "https://x/pull/1", "nia · #514 · done, PR"},
		{"nia", "", StateBusy, "", "nia · working"},
		{"nia", "#514", StateLimited, "", "nia · #514 · limited"},
	} {
		if got := slotTitle(c.slot, c.task, c.state, c.pr); got != c.want {
			t.Errorf("slotTitle(%+v) = %q, want %q", c, got, c.want)
		}
	}
}

func TestDispatchTaskLabelsThePane(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := newLabelHost()
	round := 10
	if _, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "#514", Round: &round}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.titles, ","); got != "w1=w1 · #514 · working" {
		t.Errorf("titles = %q", got)
	}
	if len(f.tokens) != 1 || f.tokens[0] != "r10 1 slots: 1 working, 0 blocked, 0 done" {
		t.Errorf("tokens = %q", f.tokens)
	}
}

func TestDispatchRelayDoesNotLabel(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := newLabelHost()
	if _, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "#514"}); err != nil {
		t.Fatal(err)
	}
	f.titles, f.tokens = nil, nil
	if _, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "more\n"), Relay: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.titles) != 0 {
		t.Errorf("a relay must not relabel: %q", f.titles)
	}
}

func TestPollRelabelsOnAStateChangeOnly(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 2, Base: "main"})
	f := newLabelHost()
	f.panes, f.status = map[string][]string{}, map[string]string{}
	f.panes["w1"] = []string{"static\n", "static\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	f.panes["w2"] = []string{"a\n", "a\n"} // idle, as recorded: no change
	if _, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.titles, ","); got != "w1=w1 · done, PR" {
		t.Errorf("titles = %q", got)
	}
	if len(f.tokens) != 1 {
		t.Errorf("tokens = %q", f.tokens)
	}
	// The same poll again: nothing changed, nothing is set.
	f.titles, f.tokens = nil, nil
	f.panes["w1"] = []string{"x\nROTA-DONE w1 https://github.com/o/r/pull/9\n", "x\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	if _, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if len(f.titles) != 0 {
		t.Errorf("an unchanged poll must not label: %q", f.titles)
	}
}

func TestWaitRelabelsTheReturnedSlot(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := newLabelHost()
	f.panes, f.status = map[string][]string{}, map[string]string{}
	if _, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "#514"}); err != nil {
		t.Fatal(err)
	}
	f.titles = nil
	f.panes["w1"] = []string{"s\n", "s\nROTA-BLOCKED w1: which?\n"}
	res, err := envWith(f).Wait(bg, dir, WaitOpts{Lines: 60})
	if err != nil || res.State != StateBlocked {
		t.Fatalf("%+v %v", res, err)
	}
	if got := strings.Join(f.titles, ","); got != "w1=w1 · #514 · blocked" {
		t.Errorf("titles = %q", got)
	}
}

func TestClearLabelClearsASlotWithAHandle(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := newLabelHost()
	e := envWith(f)
	e.ClearLabel(bg, dir, "nope") // not in the pool
	if len(f.titles) != 0 {
		t.Fatalf("unknown slot labelled: %q", f.titles)
	}
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "#514"}); err != nil {
		t.Fatal(err)
	}
	f.titles = nil
	e.ClearLabel(bg, dir, "w1")
	if got := strings.Join(f.titles, ","); got != "w1=" {
		t.Errorf("titles = %q", got)
	}
	Env{}.ClearLabel(bg, dir, "w1") // nil NewHost: no-op, no panic
}

func TestReapClearsTheLabelBeforeDropping(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	f := newLabelHost()
	e := envWith(f)
	if _, err := e.Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "#514"}); err != nil {
		t.Fatal(err)
	}
	f.titles = nil
	if _, err := e.Reap(dir, []string{"w1"}, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.titles, ","); got != "w1=" {
		t.Errorf("titles = %q", got)
	}
}
