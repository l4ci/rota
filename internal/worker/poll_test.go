package worker

import (
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/stamp"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/harness"
	"time"
)

func pollRegistry(t *testing.T, kind host.Kind) (string, *fakeHost) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 2, Base: "main"})
	f := &fakeHost{name: kind, inSession: true, panes: map[string][]string{}, status: map[string]string{}}
	return dir, f
}

func TestPollRecordsStateAndPRURL(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w1"] = []string{"static\n", "static\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	before, _ := os.ReadFile(RegistryPath(dir))
	res, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Slots) != 2 || res.Slots[0] != (PollRow{"w1", StateDone, "https://github.com/o/r/pull/9"}) ||
		res.Slots[1] != (PollRow{"w2", StateIdle, "static pane, no sentinel"}) || !res.Changed {
		t.Errorf("%+v", res)
	}
	if got := slotField(t, dir, "w1", "state"); got != "done" {
		t.Errorf("state = %s", got)
	}
	if got := slotField(t, dir, "w1", "pr"); got != "https://github.com/o/r/pull/9" {
		t.Errorf("pr = %s", got)
	}
	if got := slotField(t, dir, "w2", "pr"); got != "<null>" {
		t.Errorf("pr = %s", got)
	}
	after, _ := os.ReadFile(RegistryPath(dir))
	if string(before) == string(after) {
		t.Error("registry unchanged")
	}
	// the same poll again changes nothing
	f.panes["w1"] = []string{"x\nROTA-DONE w1 https://github.com/o/r/pull/9\n", "x\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	if res, _ = envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); res.Changed {
		t.Error("an unchanged poll must report changed=false")
	}
}

// A reader of the registry never sees done without the evidence the same done
// line carried: the state and the PR (or issues list) share one write.
func TestPollRecordsDoneAndEvidenceInOneWrite(t *testing.T) {
	for _, tc := range []struct{ line, field string }{
		{"ROTA-DONE w1 https://github.com/o/r/pull/9", "pr"},
		{"ROTA-DONE w1 issues:#4,#5", "issues"},
	} {
		dir, f := pollRegistry(t, "tmux")
		f.panes["w1"] = []string{"x\n", "x\n" + tc.line + "\n"}
		f.panes["w2"] = []string{"a\n", "a\n"}
		stop, bad := make(chan struct{}), make(chan string, 1)
		go func() {
			defer close(bad)
			for {
				select {
				case <-stop:
					return
				default:
				}
				reg, err := LoadRegistry(dir)
				if err != nil {
					continue
				}
				if s := reg.Slot("w1"); s != nil && s.State() == "done" && s.PR() == "" && len(s.Issues()) == 0 {
					bad <- "done recorded without evidence"
					return
				}
			}
		}()
		_, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60})
		close(stop)
		if err != nil {
			t.Fatal(err)
		}
		if msg, ok := <-bad; ok {
			t.Errorf("%s: %s", tc.field, msg)
		}
		reg := LoadRegistryTolerant(dir)
		if s := reg.Slot("w1"); s.State() != "done" || (s.PR() == "" && len(s.Issues()) == 0) {
			t.Errorf("%s: final state %s pr=%q issues=%v", tc.field, s.State(), s.PR(), s.Issues())
		}
	}
}

// A branch name after ROTA-DONE must not become slot.pr: `gh pr merge` on a
// branch fails where the gate's local merge would have worked.
func TestPollOnlyURLShapedDoneBecomesThePR(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w1"] = []string{"x\n", "ROTA-DONE w1 rota-worker/w1-t1\n"}
	f.panes["w2"] = []string{"x\n", "ROTA-DONE w2 https://example.com/not/a/pr\n"}
	envWith(f).Poll(bg, dir, PollOpts{Lines: 60})
	for _, s := range []string{"w1", "w2"} {
		if got := slotField(t, dir, s, "pr"); got != "<null>" {
			t.Errorf("%s pr = %s", s, got)
		}
		if got := slotField(t, dir, s, "state"); got != "done" {
			t.Errorf("%s state = %s", s, got)
		}
	}
}

func TestPollNamedSlotAndSettle(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w2"] = []string{"a\n", "b\n"}
	res, err := envWith(f).Poll(bg, dir, PollOpts{Slot: "w2", Lines: 60})
	if err != nil || len(res.Slots) != 1 || res.Slots[0].State != StateBusy {
		t.Fatalf("%+v %v", res, err)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "w1") {
			t.Errorf("polled an unnamed slot: %v", f.calls)
		}
	}
	if _, err = envWith(f).Poll(bg, dir, PollOpts{Slot: "w9"}); exitOf(err) != exitcode.ExitResolution {
		t.Errorf("unknown slot: %v", err)
	}
}

func TestPollNotifiesOnTheTransitionOnly(t *testing.T) {
	dir, f := pollRegistry(t, "herdr")
	f.status["w1"] = "blocked"
	f.panes["w1"] = []string{"dialog\n", "dialog\n", "dialog\n", "dialog\n"}
	f.panes["w2"] = []string{"x\n", "x\n", "x\n", "x\n"}
	e := envWith(f)
	if _, err := e.Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "notify rota worker w1: NEEDS-PERMISSION") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("notifications = %d, want 1 (a poll loop must not ring every few seconds): %v", n, f.calls)
	}
	if got := slotField(t, dir, "w1", "state"); got != "needs-permission" {
		t.Errorf("state = %s", got)
	}
}

func TestPollHostFailureAndNoRegistry(t *testing.T) {
	dir := newProject(t, `{}`)
	f := tmuxFake()
	res, err := envWith(f).Poll(bg, dir, PollOpts{})
	if err != nil || len(res.Slots) != 0 || res.Changed {
		t.Errorf("no registry: %+v %v", res, err)
	}
	f.requireErr = fmt.Errorf("tmux is not installed")
	if _, err = envWith(f).Poll(bg, dir, PollOpts{}); exitOf(err) != exitcode.ExitUnavailable {
		t.Errorf("host missing: %v", err)
	}
}

// activeAt is the stall clock of `round reconcile`: dispatch stamps it, poll
// re-stamps it only when the classified state differs from the recorded one.
func TestActiveAtStampedByDispatchAndRestampedByPollOnAStateChange(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w1"] = []string{"? for shortcuts\n"}
	const dispatched = "2026-10-02T15:04:05Z"
	if _, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "go\n"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "activeAt"); got != dispatched {
		t.Fatalf("dispatch stamps activeAt: %q", got)
	}
	if got := slotField(t, dir, "w2", "activeAt"); got != "<null>" {
		t.Errorf("a slot never dispatched has none: %q", got)
	}

	later := envWith(f)
	later.Now = func() time.Time { return time.Date(2026, 10, 2, 16, 0, 0, 0, time.UTC) }
	// busy -> busy: the pane moves, the state is the recorded one: no restamp.
	f.panes["w1"] = []string{"a\n", "b\n"}
	if _, err := later.Poll(bg, dir, PollOpts{Slot: "w1", Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "activeAt"); got != dispatched {
		t.Errorf("an unchanged state must not restamp: %q", got)
	}
	// busy -> done: a change.
	f.panes["w1"] = []string{"x\n", "x\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	if _, err := later.Poll(bg, dir, PollOpts{Slot: "w1", Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "activeAt"); got != "2026-10-02T16:00:00Z" {
		t.Errorf("a state change restamps: %q", got)
	}
}

// A review item's done line names the issues it filed; Poll records them on the
// slot and leaves slot.pr empty.
func TestPollRecordsIssuesDone(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w1"] = []string{"x\n", "ROTA-DONE w1 issues:#139,#140\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	if _, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").Issues(); strings.Join(got, ",") != "#139,#140" {
		t.Errorf("issues = %v", got)
	}
	if got := slotField(t, dir, "w1", "pr"); got != "<null>" {
		t.Errorf("pr = %s", got)
	}
	if got := slotField(t, dir, "w1", "state"); got != "done" {
		t.Errorf("state = %s", got)
	}
}

// A worker that opened a PR but printed no URL leaves the slot without one;
// poll records the open PR headed by the slot's branch (#210).
func TestOpenPRsByHeadRecordsOnlyUnrecordedSlots(t *testing.T) {
	w := newWorld(t, "")
	w.forge("listed", "1")
	reg := LoadRegistryTolerant(w.dir)
	polled := map[string]PollRow{"w1": {Name: "w1", State: StateIdle}}
	if got := w.env(false).WithDefaults().openPRsByHead(bg, w.dir, reg, polled)["w1"]; got != ghURL {
		t.Errorf("w1 -> %q", got)
	}
	// an unpolled slot, or one that already records its PR, needs no listing
	w.forge("listError", "must not be called")
	if got := w.env(false).WithDefaults().openPRsByHead(bg, w.dir, reg, nil); got != nil {
		t.Errorf("unpolled: %v", got)
	}
	w.setSlot(ghURL, "")
	if got := w.env(false).WithDefaults().openPRsByHead(bg, w.dir, LoadRegistryTolerant(w.dir), polled); got != nil {
		t.Errorf("recorded: %v", got)
	}
}

// An idle slot with no sentinel whose branch heads an open PR is DONE with that
// PR and says the sentinel was missing (#227). No PR, or a listing that fails,
// leaves it idle (stuck) as before.
func TestPromoteIdleWithPR(t *testing.T) {
	idle := func() []PollRow { return []PollRow{{"w1", StateIdle, "static pane, no sentinel"}} }
	w := newWorld(t, "")

	w.forge("listed", "1")
	rows, notes := w.env(false).WithDefaults().promoteIdleWithPR(bg, w.dir, idle())
	if rows[0] != (PollRow{"w1", StateDone, ghURL}) || notes["w1"] != missingSentinelNote {
		t.Errorf("open PR: %+v %v", rows, notes)
	}

	w.forge("listed", "0")
	rows, notes = w.env(false).WithDefaults().promoteIdleWithPR(bg, w.dir, idle())
	if rows[0].State != StateIdle || notes != nil {
		t.Errorf("no PR: %+v %v", rows, notes)
	}

	w.forge("listError", "boom")
	rows, notes = w.env(false).WithDefaults().promoteIdleWithPR(bg, w.dir, idle())
	if rows[0].State != StateIdle || notes != nil {
		t.Errorf("lookup error: %+v %v", rows, notes)
	}

	// only an idle row is looked at
	w.forge("listed", "1")
	busy := []PollRow{{"w1", StateBusy, "x"}}
	if rows, notes = w.env(false).WithDefaults().promoteIdleWithPR(bg, w.dir, busy); rows[0].State != StateBusy || notes != nil {
		t.Errorf("busy: %+v %v", rows, notes)
	}
}

// A transferred slot inherits an open PR it has not touched: its idle pane is
// not a worker that stopped after opening it. Only the slot's own done clears
// the mark, and the promotion resumes (#2074).
func TestPromoteIdleSkipsATransferredSlotUntilItReportsDone(t *testing.T) {
	idle := func() []PollRow { return []PollRow{{"w1", StateIdle, "static pane, no sentinel"}} }
	w := newWorld(t, "")
	w.forge("listed", "1")
	if _, err := UpdateSlot(w.dir, "w1", func(s *Slot) { s.Raw().Set("awaitingDone", true) }); err != nil {
		t.Fatal(err)
	}
	rows, notes := w.env(false).WithDefaults().promoteIdleWithPR(bg, w.dir, idle())
	if rows[0].State != StateIdle || notes != nil {
		t.Errorf("transferred slot promoted on the old head: %+v %v", rows, notes)
	}
}

// A slot herdr cannot classify carries herdr's own explanation; a failing
// explain leaves today's evidence and the poll still succeeds.
func TestPollUnknownCarriesExplainExcerpt(t *testing.T) {
	dir, f := pollRegistry(t, "herdr")
	f.status["w1"], f.status["w2"] = "unknown", "unknown"
	f.explain = map[string]string{"w1": "no recognised agent process / foreground is vim"}
	f.panes["w1"] = []string{"x\n", "x\n"}
	f.panes["w2"] = []string{"x\n", "x\n"}
	res, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60})
	if err != nil {
		t.Fatal(err)
	}
	const base = "herdr cannot classify the agent — inspect the tab; not proof it finished"
	if got := res.Slots[0]; got.State != StateUnknown || got.Evidence != base+": herdr explain: no recognised agent process / foreground is vim" {
		t.Errorf("w1 = %+v", got)
	}
	if got := res.Slots[1]; got.State != StateUnknown || got.Evidence != base {
		t.Errorf("a failing explain must leave the evidence unchanged: %+v", got)
	}
}

// The review cursor starts when the slot reports done: comments and bot posts
// from before the PR was handed back are never review input. A re-done after a
// relay keeps the cursor the relay moved.
func TestPollBaselinesTheReviewCursorAtDone(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	f.panes["w1"] = []string{"x\n", "x\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	if _, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	seen := slotField(t, dir, "w1", "reviewSeen")
	if seen == "<null>" || seen == "" {
		t.Fatalf("no baseline at done: %q", seen)
	}
	if got := slotField(t, dir, "w2", "reviewSeen"); got != "<null>" {
		t.Errorf("a busy slot got a baseline: %q", got)
	}
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetReviewSeen("2026-10-08T10:00:00Z") }); err != nil {
		t.Fatal(err)
	}
	f.panes["w1"] = []string{"y\nROTA-DONE w1 https://github.com/o/r/pull/9\n", "y\nROTA-DONE w1 https://github.com/o/r/pull/9\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	if _, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "reviewSeen"); got != "2026-10-08T10:00:00Z" {
		t.Errorf("a later poll moved the cursor: %q", got)
	}
}

// The pane's last ROTA-DONE outlives its issue: a slot that took the next issue
// would read the previous PR back as its own while that PR waits in `prs` (#648).
func TestPollDoesNotRecordAPRAnotherRecordHolds(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	const url = "https://github.com/o/r/pull/9"
	if err := Update(dir, func(d *Doc) { d.QueuePR(QueuedPR{Issue: "12", PR: url, From: "w1", Branch: "w1/12-x"}) }); err != nil {
		t.Fatal(err)
	}
	f.panes["w1"] = []string{"x\nROTA-DONE w1 " + url + "\n", "x\nROTA-DONE w1 " + url + "\n"}
	f.panes["w2"] = []string{"a\n", "a\n"}
	if _, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if got := slotField(t, dir, "w1", "pr"); got != "<null>" {
		t.Errorf("a queued PR is not the slot's: pr = %s", got)
	}
}

// A blocked event moves the slot to blocked when it is sent, so the pane
// poll never sees a transition: the event itself must ring, once.
func TestPollNotifiesABlockedEventOnce(t *testing.T) {
	dir, f := pollRegistry(t, "herdr")
	f.panes["w1"] = []string{"x\n", "x\n", "x\n", "x\n", "x\n", "x\n"}
	f.panes["w2"] = []string{"x\n", "x\n", "x\n", "x\n", "x\n", "x\n"}
	send := func(body string) {
		if err := Update(dir, func(d *Doc) {
			s := d.Slot("w1")
			now := stamp.Format(time.Now())
			s.SetEvent(EventBlocked, body, now)
			s.MarkState("blocked", now)
		}); err != nil {
			t.Fatal(err)
		}
	}
	rings := func() (n int) {
		for _, c := range f.calls {
			if strings.HasPrefix(c, "notify rota worker w1: BLOCKED") {
				n++
			}
		}
		return n
	}
	e := envWith(f)
	send("A or B?")
	for i := 0; i < 2; i++ {
		if _, err := e.Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
			t.Fatal(err)
		}
	}
	if n := rings(); n != 1 {
		t.Fatalf("notifications = %d, want 1 for one event: %v", n, f.calls)
	}
	send("C or D?")
	if _, err := e.Poll(bg, dir, PollOpts{Lines: 60}); err != nil {
		t.Fatal(err)
	}
	if n := rings(); n != 2 {
		t.Errorf("notifications = %d, want 2 after a second event", n)
	}
}

// The wiring from the registry to the classifier: slotTarget hands a slot's
// harness kind to ClassifyFor, so an opencode slot reads OpenCode's expired-key
// pane as limited and a claude slot showing the same text stays idle.
func TestPollReadsEachSlotsPaneByItsHarnessKind(t *testing.T) {
	dir, f := pollRegistry(t, "tmux")
	if err := Update(dir, func(d *Doc) { d.Slot("w1").Raw().Set("kind", harness.OpenCode) }); err != nil {
		t.Fatal(err)
	}
	pane := opencodePane(t, "expired-key")
	f.panes["w1"] = []string{pane, pane}
	f.panes["w2"] = []string{pane, pane}
	res, err := envWith(f).Poll(bg, dir, PollOpts{Lines: 60})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Slots) != 2 || res.Slots[0].State != StateLimited || res.Slots[1].State != StateIdle {
		t.Errorf("opencode slot w1, claude slot w2: %+v", res.Slots)
	}
}

// The receiver's own done (a recorded row, not a forge promotion) ends the wait.
func TestRecordRowDoneClearsAwaitingDone(t *testing.T) {
	s := NewSlot("w1", "b", "wt", "main", "h")
	s.SetAwaitingDone(true)
	if _, err := recordRow(s, PollRow{"w1", StateBusy, "x"}, slotTarget(s), time.Now(), Registry{}); err != nil || !s.AwaitingDone() {
		t.Fatalf("busy keeps the mark: %v %v", err, s.AwaitingDone())
	}
	if _, err := recordRow(s, PollRow{"w1", StateDone, "ROTA-DONE"}, slotTarget(s), time.Now(), Registry{}); err != nil || s.AwaitingDone() {
		t.Errorf("done clears the mark: %v %v", err, s.AwaitingDone())
	}
}
