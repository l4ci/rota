package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

// waitHost is a host whose panes and native status the test steers. With
// events set it is also a host.Watcher; each value sent is one status change
// of the named slot.
type waitHost struct {
	*fakeHost
	mu      sync.Mutex
	text    map[string]string // pane text per slot, "" is a static idle pane
	events  chan string
	watched []host.WatchTarget
	watchOK error
	// watchFn, when set, supplies the Nth (1-based) subscription.
	watchFn func(n int) (host.Watch, error)
	watches int
	// steps run on each Capture of a slot, to script a pane that changes.
	onCapture func(slot string)
}

func (h *waitHost) Capture(_ context.Context, slot, _ string, _ int) string {
	if h.onCapture != nil {
		h.onCapture(slot)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.text[slot]
}

func (h *waitHost) Status(_ context.Context, slot, _ string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status[slot]
}

func (h *waitHost) set(slot, text, native string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.text[slot], h.status[slot] = text, native
}

type waitWatch struct{ h *waitHost }

func (w waitWatch) Next(ctx context.Context) (string, error) {
	select {
	case s, ok := <-w.h.events:
		if !ok {
			return "", fmt.Errorf("%w: closed", host.ErrStreamLost)
		}
		return s, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (waitWatch) Close() {}

// doneOnNext runs arm on its first Next, which arm answers with an event.
type doneOnNext struct {
	h   *waitHost
	arm func()
}

func (d doneOnNext) Next(ctx context.Context) (string, error) {
	d.arm()
	return waitWatch{d.h}.Next(ctx)
}
func (doneOnNext) Close() {}

// watcherHost adds Watch; tmux-style tests use waitHost alone.
type watcherHost struct{ *waitHost }

func (h watcherHost) Watch(_ context.Context, t []host.WatchTarget) (host.Watch, error) {
	h.watched = t
	h.waitHost.watched = t
	if h.watchFn != nil {
		h.waitHost.watches++
		return h.watchFn(h.waitHost.watches)
	}
	return waitWatch{h.waitHost}, h.watchOK
}

func newWaitHost(name string) *waitHost {
	f := tmuxFake()
	f.name = name
	f.status = map[string]string{}
	return &waitHost{fakeHost: f, text: map[string]string{}, events: make(chan string, 8)}
}

func withHandles(t *testing.T, dir string, handles map[string]string) {
	t.Helper()
	if err := Update(dir, func(d *Doc) {
		for _, s := range d.Slots() {
			if h, ok := handles[s.Name()]; ok {
				s.SetHandle(h)
				s.MarkState("busy", "")
			} else {
				s.SetHandle("") // a slot never dispatched has no session
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func waitProject(t *testing.T, slots int, handles map[string]string) string {
	t.Helper()
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: slots, Base: "main"})
	withHandles(t, dir, handles)
	return dir
}

func TestWaitReturnsASlotThatAlreadyNeedsAttention(t *testing.T) {
	dir := waitProject(t, 2, map[string]string{"w1": "w9:t1", "w2": "w9:t2"})
	h := newWaitHost("herdr")
	h.set("w1", "working...\n", "working")
	h.set("w2", "ROTA-BLOCKED w2: A or B?\n", "idle")
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Slot != "w2" || res.State != StateBlocked || res.Evidence != "A or B?" || res.Source != SourceSnapshot {
		t.Errorf("%+v", res)
	}
	if len(h.watched) != 2 {
		t.Errorf("watched = %v, want both slots", h.watched)
	}
}

func TestWaitWakesOnAnEventAndReclassifies(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	h.set("w1", "working...\n", "working")
	go func() {
		time.Sleep(20 * time.Millisecond)
		h.events <- "w1" // status changed, but the pane still moves: nothing to return
		time.Sleep(20 * time.Millisecond)
		h.set("w1", "ROTA-DONE w1 https://github.com/o/r/pull/9\n", "done")
		h.events <- "w1"
	}()
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.Slot != "w1" || res.State != StateDone || res.Source != SourceEvent {
		t.Errorf("%+v", res)
	}
}

func TestWaitOnAHostWithoutEventsPollsUntilTheSlotChanges(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("tmux")
	n := 0
	h.onCapture = func(string) { // the pane moves for two classifications, then settles on ROTA-DONE
		n++
		if n <= 4 {
			h.set("w1", strings.Repeat("x", n)+"\n", "")
		} else {
			h.set("w1", "ROTA-DONE w1 rota-worker/w1\n", "")
		}
	}
	res, err := envWith(h).Wait(bg, dir, WaitOpts{Settle: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StateDone || res.Source != SourcePoll {
		t.Errorf("%+v", res)
	}
}

func TestWaitTimeoutIsAnAnswerWithTheSlotStates(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	h.set("w1", "working...\n", "working")
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Timeout: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || len(res.Slots) != 1 || res.Slots[0].State != StateBusy {
		t.Errorf("%+v", res)
	}
}

func TestWaitSkipsHandlelessSlotsUnlessNamed(t *testing.T) {
	dir := waitProject(t, 2, map[string]string{"w2": "w9:t2"})
	h := newWaitHost("herdr")
	h.set("w2", "ROTA-DONE w2 x\n", "done")
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{})
	if err != nil || res.Slot != "w2" {
		t.Fatalf("%+v %v", res, err)
	}
	if len(h.watched) != 1 || h.watched[0].Slot != "w2" {
		t.Errorf("watched = %v", h.watched)
	}
	for _, name := range []string{"w1", "nope"} {
		_, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Slots: []string{name}})
		if exitOf(err) != exitcode.ExitResolution {
			t.Errorf("named %s: %v, want exit 3", name, err)
		}
	}
}

func TestWaitDoesNotWatchSlotsRecordedIdle(t *testing.T) {
	dir := waitProject(t, 2, map[string]string{"w1": "rota:w1", "w2": "rota:w2"})
	Update(dir, func(d *Doc) { // w1 was polled idle, w2 is running
		d.Slot("w1").MarkState("idle", "")
	})
	h := newWaitHost("herdr")
	h.set("w2", "ROTA-DONE w2 x\n", "done")
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{})
	if err != nil || res.Slot != "w2" || len(h.watched) != 1 {
		t.Fatalf("%+v %v watched=%v", res, err, h.watched)
	}
	// Naming a slot watches it whatever its recorded state.
	h.set("w1", "ROTA-DONE w1 x\n", "done")
	if res, err = envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Slots: []string{"w1"}}); err != nil || res.Slot != "w1" {
		t.Errorf("named: %+v %v", res, err)
	}
}

func TestWaitWithNothingToWatchIsAResolutionError(t *testing.T) {
	dir := waitProject(t, 1, nil)
	_, err := envWith(newWaitHost("tmux")).Wait(bg, dir, WaitOpts{})
	if exitOf(err) != exitcode.ExitResolution {
		t.Errorf("%v, want exit 3", err)
	}
}

func TestWaitHostFailuresAreUnavailable(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	h.watchOK = host.ErrUnsupportedHerdr
	if _, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{}); exitOf(err) != exitcode.ExitUnavailable {
		t.Errorf("watch failure: %v, want exit 5", err)
	}
	h = newWaitHost("herdr")
	h.requireErr = errors.New("herdr is not installed")
	if _, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{}); exitOf(err) != exitcode.ExitUnavailable {
		t.Errorf("not installed: %v, want exit 5", err)
	}
}

func TestWaitCancelIsNotATimeout(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	h.set("w1", "working\n", "working")
	ctx, cancel := context.WithCancel(bg)
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	res, err := envWith(watcherHost{h}).Wait(ctx, dir, WaitOpts{})
	if exitOf(err) != exitcode.ExitFailed || res.TimedOut {
		t.Errorf("%+v %v", res, err)
	}
}

// #211: herdr sends one event per status change and none after `done`. Just
// after a turn ends, herdr's `agent read --source recent-unwrapped` rebuilds
// scrollback, and two reads a moment apart can return different windows (one
// recorded read carried 34 more history lines and a different prompt line).
// The classification that follows the last event then sees "movement" and
// reads busy, and no further event ever re-classifies it. Events and panes are
// herdr 0.9.3's own, recorded in the live re-check of the slot's tab.
//
// The recorded panes end on the worker's `● ROTA-DONE` reply, which the
// classifier sees at once (#210), so the event itself settles that case. A
// worker that finishes without a sentinel is the case only the re-check can
// settle: the same panes with the reply line removed.
func TestWaitAfterTheLastEventRechecksAPaneThatMovedOnce(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile("testdata/wait-211/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	var statuses []string
	for _, line := range strings.Split(strings.TrimSpace(read("frames.jsonl")), "\n") {
		var f struct {
			Event string `json:"event"`
			Data  struct {
				Status string `json:"agent_status"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &f); err != nil || f.Event != "pane.agent_status_changed" {
			t.Fatalf("bad frame %q", line)
		}
		statuses = append(statuses, f.Data.Status)
	}
	if strings.Join(statuses, ",") != "working,done" {
		t.Fatalf("frames = %v", statuses)
	}
	const reply = "● ROTA-DONE lr2 lr2/f01-add-a-hello-line-to\n"
	scrollback, settled := read("pane-scrollback-read.txt"), read("pane-settled.txt")
	if !strings.Contains(scrollback, reply) || !strings.Contains(settled, reply) {
		t.Fatal("the recorded panes should end on the worker's ROTA-DONE reply")
	}
	noReply := func(s string) string { return strings.Replace(s, reply, "", 1) }

	for _, c := range []struct {
		name                string
		scrollback, settled string
		state, source       string
	}{
		{"recorded, sentinel seen at the event", scrollback, settled, StateDone, SourceEvent},
		{"no sentinel, settled by the re-check", noReply(scrollback), noReply(settled), StateIdle, SourcePoll},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
			h := newWaitHost("herdr")
			h.set("w1", "✻ Working…\n", statuses[0])
			captures, armed := 0, false
			h.onCapture = func(string) {
				h.mu.Lock()
				defer h.mu.Unlock()
				if armed {
					captures++
					if captures == 1 {
						h.text["w1"] = c.scrollback // first capture after the done event
					} else {
						h.text["w1"] = c.settled // later reads agree; no event says so
					}
				} else {
					h.text["w1"] += "." // a working pane moves between captures
				}
			}
			// The done event arrives only once Wait is blocked on the stream,
			// i.e. after its first classification read the working pane. A
			// timer raced that read under load, and the snapshot answered first.
			h.watchFn = func(int) (host.Watch, error) {
				return doneOnNext{h, func() {
					h.mu.Lock()
					h.status["w1"], armed = statuses[1], true
					h.mu.Unlock()
					h.events <- "w1"
				}}, nil
			}
			res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Timeout: 2 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			if res.TimedOut || res.Slot != "w1" || res.State != c.state || res.Source != c.source {
				t.Fatalf("want %s via %s, got %+v", c.state, c.source, res)
			}
		})
	}
}

func seenField(dir, slot, key string) string {
	return jsonx.Str(LoadRegistry(dir).Slot(slot).Raw(), key)
}

func TestWaitRecordsWhatItReturned(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	url := "https://github.com/o/r/pull/7"
	h.set("w1", "ROTA-DONE w1 "+url+"\n", "done")
	if _, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := seenField(dir, "w1", "state"); got != "done" {
		t.Errorf("state = %q", got)
	}
	if got := seenField(dir, "w1", "pr"); got != url {
		t.Errorf("pr = %q", got)
	}
	if got := seenField(dir, "w1", "seen"); got != seenKey(StateDone, url) {
		t.Errorf("seen = %q", got)
	}
}

func TestWaitDoesNotReturnTheSameArrivalTwice(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	h.set("w1", "ROTA-DONE w1 x\n", "done")
	e := envWith(watcherHost{h})
	if res, err := e.Wait(bg, dir, WaitOpts{}); err != nil || res.State != StateDone {
		t.Fatalf("first: %+v %v", res, err)
	}
	res, err := e.Wait(bg, dir, WaitOpts{Timeout: 30 * time.Millisecond})
	if err != nil || !res.TimedOut {
		t.Fatalf("second: %+v %v, want a timeout", res, err)
	}
	// The slot goes busy, then finishes again: it is news.
	h.set("w1", "working...\n", "working")
	if res, err = e.Wait(bg, dir, WaitOpts{Timeout: 30 * time.Millisecond}); err != nil || !res.TimedOut {
		t.Fatalf("busy: %+v %v", res, err)
	}
	if got := seenField(dir, "w1", "seen"); got != "" {
		t.Errorf("seen = %q after the slot was observed busy", got)
	}
	h.set("w1", "ROTA-DONE w1 x\n", "done")
	if res, err = e.Wait(bg, dir, WaitOpts{}); err != nil || res.State != StateDone {
		t.Fatalf("third: %+v %v", res, err)
	}
}

// A prompt answered by typing in the pane re-arms nothing, so a second one
// that wait never saw go busy in between must still come back (#29 review).
func TestWaitReturnsPromptsAndNewQuestions(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	e := envWith(watcherHost{h})
	h.set("w1", "Do you want to proceed?\n", "idle")
	if res, err := e.Wait(bg, dir, WaitOpts{}); err != nil || res.State != StateNeedsPermission {
		t.Fatalf("first prompt: %+v %v", res, err)
	}
	h.set("w1", "Allow bash to run go test?\n", "idle")
	if res, err := e.Wait(bg, dir, WaitOpts{Timeout: time.Second}); err != nil || res.State != StateNeedsPermission {
		t.Fatalf("second prompt, no busy between: %+v %v", res, err)
	}
	if got := seenField(dir, "w1", "seen"); got != "" {
		t.Errorf("a permission prompt is never remembered: seen = %q", got)
	}
	// A blocked worker comes back once per question.
	h.set("w1", "ROTA-BLOCKED w1: keep the old flag?\n", "idle")
	if res, err := e.Wait(bg, dir, WaitOpts{}); err != nil || res.State != StateBlocked {
		t.Fatalf("first question: %+v %v", res, err)
	}
	if res, err := e.Wait(bg, dir, WaitOpts{Timeout: 30 * time.Millisecond}); err != nil || !res.TimedOut {
		t.Fatalf("same question again: %+v %v, want a timeout", res, err)
	}
	h.set("w1", "ROTA-BLOCKED w1: which default?\n", "idle")
	if res, err := e.Wait(bg, dir, WaitOpts{Timeout: time.Second}); err != nil || res.State != StateBlocked || res.Evidence != "which default?" {
		t.Fatalf("new question, no busy between: %+v %v", res, err)
	}
}

func TestDispatchAndPollClearSeen(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	set := func() {
		UpdateSlot(dir, "w1", func(s *Slot) { s.MarkState("done", ""); s.SetSeen(seenKey(StateDone, "x")) })
	}
	set()
	if err := recordDispatch(dir, "w1", "w9:t1", "", "", nil, 0, "now"); err != nil {
		t.Fatal(err)
	}
	if got := seenField(dir, "w1", "seen"); got != "" {
		t.Errorf("relay kept seen = %q", got)
	}
	set()
	if err := recordDispatch(dir, "w1", "w9:t1", "#5", "", nil, 0, "now"); err != nil {
		t.Fatal(err)
	}
	if got := seenField(dir, "w1", "seen"); got != "" {
		t.Errorf("dispatch kept seen = %q", got)
	}
	// Poll recording a different state drops it; the same state keeps it.
	set()
	h := newWaitHost("herdr")
	h.set("w1", "ROTA-DONE w1 x\n", "done")
	if _, err := envWith(h).Poll(bg, dir, PollOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := seenField(dir, "w1", "seen"); got != seenKey(StateDone, "x") {
		t.Errorf("poll of the same state dropped seen: %q", got)
	}
	h.set("w1", "working...\n", "working")
	if _, err := envWith(h).Poll(bg, dir, PollOpts{}); err != nil {
		t.Fatal(err)
	}
	if got := seenField(dir, "w1", "seen"); got != "" {
		t.Errorf("poll of a new state kept seen = %q", got)
	}
}

func TestSoloWaitReturnsAnArrivalOnce(t *testing.T) {
	dir := soloProject(t)
	setState(t, dir, "w1", "done")
	res, err := soloWait(dir, WaitOpts{Slots: []string{"w1"}})
	if err != nil || res.Slot != "w1" || res.State != "done" {
		t.Fatalf("first: %+v %v", res, err)
	}
	if res, err = soloWait(dir, WaitOpts{Slots: []string{"w1"}}); err != nil || !res.TimedOut {
		t.Fatalf("second: %+v %v, want timed out", res, err)
	}
}

// stateForge answers PRView with a fixed state; the rest of Forge is unused.
type stateForge struct {
	Forge
	state string
}

func (f stateForge) PRView(context.Context, int) (tracker.PRInfo, error) {
	return tracker.PRInfo{State: f.state}, nil
}

// A done slot whose PR is merged is not news: the orchestrator has nothing to
// review, so every re-armed watch would end at once on it (#327).
func TestWaitIgnoresADoneSlotWhoseRecordedPRIsMerged(t *testing.T) {
	for state, wantNews := range map[string]bool{"MERGED": false, "OPEN": true} {
		dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
		gittest.Run(t, dir, "remote", "add", "origin", "https://github.com/o/r.git")
		if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetPR("https://github.com/o/r/pull/9") }); err != nil {
			t.Fatal(err)
		}
		h := newWaitHost("herdr")
		h.set("w1", "ROTA-DONE w1 https://github.com/o/r/pull/9\n", "done")
		e := envWith(watcherHost{h})
		e.Forge = func(string, string, any) (Forge, error) { return stateForge{state: state}, nil }
		res, err := e.Wait(bg, dir, WaitOpts{Timeout: 30 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		if got := !res.TimedOut; got != wantNews {
			t.Errorf("PR %s: news = %v, want %v (%+v)", state, got, wantNews, res)
		}
	}
}

// lostWatch is a subscription whose first Next reports the loss.
type lostWatch struct {
	err   error
	onGap func() // runs as the stream drops
}

func (w lostWatch) Next(context.Context) (string, error) {
	if w.onGap != nil {
		w.onGap()
	}
	return "", w.err
}
func (lostWatch) Close() {}

// blockWatch never delivers; Next ends with its context.
type blockWatch struct{}

func (blockWatch) Next(ctx context.Context) (string, error) { <-ctx.Done(); return "", ctx.Err() }
func (blockWatch) Close()                                   {}

// #510: events_lost and a closed stream re-subscribe instead of ending the
// wait, and a transition during the gap is read from current state, once.
func TestWaitResubscribesAfterALostStream(t *testing.T) {
	for name, cause := range map[string]error{
		"events_lost": fmt.Errorf("%w: events_lost", host.ErrStreamLost),
		"eof":         fmt.Errorf("%w: herdr closed the event stream", host.ErrStreamLost),
	} {
		t.Run(name, func(t *testing.T) {
			dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
			h := newWaitHost("herdr")
			h.set("w1", "working\n", "working")
			h.watchFn = func(n int) (host.Watch, error) {
				if n == 1 {
					return lostWatch{cause, func() { h.set("w1", "ROTA-DONE w1 https://x/pr/1\n", "idle") }}, nil // changes while disconnected
				}
				return blockWatch{}, nil
			}
			var errBuf strings.Builder
			e := envWith(watcherHost{h})
			e.Stderr = &errBuf
			res, err := e.Wait(bg, dir, WaitOpts{})
			if err != nil || res.Slot != "w1" || res.State != StateDone {
				t.Fatalf("%+v %v", res, err)
			}
			if h.watches != 2 || errBuf.Len() != 0 {
				t.Errorf("watches = %d, stderr = %q; want one re-subscribe and silence", h.watches, errBuf.String())
			}
			// Reported once: the same state is not news on the next wait.
			again, err := e.Wait(bg, dir, WaitOpts{Timeout: 50 * time.Millisecond})
			if err != nil || !again.TimedOut {
				t.Errorf("second wait = %+v %v, want timed out", again, err)
			}
		})
	}
}

// Repeated loss falls back to the poll path, says so once, and never spins on
// the socket.
func TestWaitFallsBackToPollingAfterRepeatedLoss(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	h.set("w1", "working\n", "working")
	h.watchFn = func(n int) (host.Watch, error) {
		if n == 1 {
			return lostWatch{err: host.ErrStreamLost}, nil
		}
		return nil, errors.New("herdr socket: connection refused")
	}
	captures := 0
	h.onCapture = func(string) {
		if captures++; captures == 8 {
			h.set("w1", "ROTA-DONE w1 https://x/pr/1\n", "idle")
		}
	}
	var errBuf strings.Builder
	e := envWith(watcherHost{h})
	e.Stderr = &errBuf
	res, err := e.Wait(bg, dir, WaitOpts{})
	if err != nil || res.State != StateDone || res.Source != SourcePoll {
		t.Fatalf("%+v %v", res, err)
	}
	if h.watches != 1+maxResubscribe {
		t.Errorf("watches = %d, want %d", h.watches, 1+maxResubscribe)
	}
	if n := strings.Count(errBuf.String(), "polling instead"); n != 1 {
		t.Errorf("fallback notice printed %d times: %q", n, errBuf.String())
	}
}

// #707: a dispatch that replaces a slot's session while a wait is classifying
// it must not leave the old pane's death on the new session.
func TestWaitIgnoresADeathReadFromAReplacedHandle(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:old"})
	h := newWaitHost("herdr")
	h.set("w1", "", "gone") // the killed pane
	captures := 0
	h.onCapture = func(string) {
		captures++
		switch captures {
		case 1: // dispatch lands mid-classification: new handle, same pane read
			if err := recordDispatch(dir, "w1", "w9:new", "42", "", nil, 0, "2026-10-02T15:04:05Z"); err != nil {
				t.Error(err)
			}
		case 3: // the next classification reads the new, live session
			h.mu.Lock()
			h.text["w1"], h.status["w1"] = "working...\n", "working"
			h.mu.Unlock()
		}
	}
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Timeout: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("a replaced session's death was reported: %+v", res)
	}
	if s := LoadRegistry(dir).Slot("w1"); s.State() != "busy" || s.Handle() != "w9:new" {
		t.Errorf("slot = %s on %s, want busy on w9:new", s.State(), s.Handle())
	}
}

func TestPollIgnoresADeathReadFromAReplacedHandle(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:old"})
	h := newWaitHost("herdr")
	h.set("w1", "", "gone")
	var once sync.Once
	h.onCapture = func(string) {
		once.Do(func() {
			if err := recordDispatch(dir, "w1", "w9:new", "42", "", nil, 0, "2026-10-02T15:04:05Z"); err != nil {
				t.Error(err)
			}
		})
	}
	if _, err := envWith(watcherHost{h}).Poll(bg, dir, PollOpts{}); err != nil {
		t.Fatal(err)
	}
	if s := LoadRegistry(dir).Slot("w1"); s.State() != "busy" || s.Handle() != "w9:new" {
		t.Errorf("slot = %s on %s, want busy on w9:new", s.State(), s.Handle())
	}
}

// spawnProbe reads the registry at the moment the new session is spawned.
type spawnProbe struct {
	*fakeHost
	dir    string
	handle *string
}

func (p spawnProbe) Spawn(ctx context.Context, o host.SpawnOpts) (string, error) {
	*p.handle = LoadRegistry(p.dir).Slot(o.Slot).Handle()
	return p.fakeHost.Spawn(ctx, o)
}

// #707: between the kill and the new session's record, the registry must not
// hold the killed pane's handle, or a watch tick reads it as a dead slot.
func TestDispatchDropsTheKilledHandleBeforeSpawning(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:old"})
	seen := "unset"
	h := spawnProbe{tmuxFake(), dir, &seen}
	if _, err := envWith(h).Dispatch(bg, dir, DispatchOpts{Slot: "w1", BodyFile: writeBrief(t, "task\n"), Task: "T1"}); err != nil {
		t.Fatal(err)
	}
	if seen != "" {
		t.Errorf("handle during spawn = %q, want none", seen)
	}
	if got := LoadRegistry(dir).Slot("w1").Handle(); got != "w9:t7" {
		t.Errorf("handle after dispatch = %q, want w9:t7", got)
	}
}
