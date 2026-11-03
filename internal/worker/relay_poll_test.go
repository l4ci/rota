package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/host"
)

// The relay can scroll out between the two captures of the very first poll.
// Neither a new commit nor a worker done event is needed for a pane reply.
func TestPollRelayEchoScrollsOutBetweenCaptures(t *testing.T) {
	for _, done := range []string{"ROTA-DONE w1\n", "ROTA-DONE w1 https://github.com/o/r/pull/994\n"} {
		t.Run(strings.TrimSpace(done), func(t *testing.T) {
			dir, h := pollRegistry(t, "tmux")
			relayRaceSlot(t, dir, 1)
			h.panes["w1"] = []string{relayEcho(1), strings.Repeat("● work\n", 80) + done}
			res, err := envWith(h).Poll(bg, dir, PollOpts{Slot: "w1", Lines: 60})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Slots) != 1 || res.Slots[0].State != StateDone {
				t.Fatalf("fresh done after relay scrolled out: %+v", res)
			}
			s := LoadRegistryTolerant(dir).Slot("w1")
			if s.State() != "done" || s.RelayPending() != 0 || s.RelayMarkerSeen() {
				t.Fatalf("recorded state=%s pending=%d seen=%v", s.State(), s.RelayPending(), s.RelayMarkerSeen())
			}
		})
	}
}

type relayHistoryHost struct {
	*fakeHost
	recent, history string
	historyErr      error
}

func (h *relayHistoryHost) Capabilities() host.Capabilities { return host.CapabilitiesOf(h) }
func (h *relayHistoryHost) ReadPane(_ context.Context, _, _ string, lines int) (string, error) {
	if lines > 60 {
		return h.history, h.historyErr
	}
	return h.recent, nil
}

func TestPollRelayEchoAlreadyOutsideFirstCapture(t *testing.T) {
	const done = "ROTA-DONE w1 https://github.com/o/r/pull/994\n"
	for name, tc := range map[string]struct {
		history string
		err     error
		want    string
	}{
		"retained history": {relayEcho(1) + strings.Repeat("● work\n", 80) + done, nil, StateDone},
		"old done":         {done + relayEcho(1), nil, StateBusy},
		"wrong relay":      {relayEcho(2) + done, nil, StateBusy},
		"quoted done":      {"❯ --- ORCHESTRATOR (round 27) ---\n  [relay 1]\n  " + done, nil, StateBusy},
		"no history":       {done, nil, StateBusy},
		"failed history":   {relayEcho(1) + done, errors.New("history unavailable"), StateBusy},
	} {
		t.Run(name, func(t *testing.T) {
			dir, f := pollRegistry(t, "herdr")
			relayRaceSlot(t, dir, 1)
			h := &relayHistoryHost{fakeHost: f, recent: done, history: tc.history, historyErr: tc.err}
			res, err := envWith(h).Poll(bg, dir, PollOpts{Slot: "w1", Lines: 60})
			if err != nil || len(res.Slots) != 1 || res.Slots[0].State != tc.want {
				t.Fatalf("first poll with echo outside recent output: %+v, %v; want %s", res, err, tc.want)
			}
			s := LoadRegistryTolerant(dir).Slot("w1")
			if tc.want == StateDone && (s.State() != "done" || s.RelayPending() != 0) {
				t.Fatalf("fresh completion was not recorded: %v", s.Raw())
			}
			if tc.want == StateBusy && (s.State() != "busy" || s.RelayPending() != 1 || s.RelayMarkerSeen()) {
				t.Fatalf("unproven completion released relay: %v", s.Raw())
			}
		})
	}
}

func TestWaitRefreshesPendingRelayBetweenPasses(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	relayRaceSlot(t, dir, 1)
	h := newWaitHost("herdr")
	h.set("w1", relayEcho(1), "working")
	pass := 0
	relayCaptures := 0
	h.onCapture = func(string) {
		if pass == 1 {
			relayCaptures++
			if relayCaptures == 1 {
				h.set("w1", relayEcho(2), "working")
			} else {
				h.set("w1", strings.Repeat("● work\n", 80), "working")
			}
		}
	}
	h.watchFn = func(int) (host.Watch, error) {
		return doneOnNext{h, func() {
			pass++
			if pass == 1 {
				if _, err := UpdateSlot(dir, "w1", func(s *Slot) {
					s.AppendRelay(newRelayEntry(27, time.Now(), "second reply"))
					s.SetRelayPending(2, "2026-11-03T10:01:00Z")
				}); err != nil {
					t.Fatal(err)
				}
			} else {
				h.set("w1", "ROTA-DONE w1\n", "idle")
			}
			h.events <- "w1"
		}}, nil
	}
	res, err := envWith(watcherHost{h}).Wait(context.Background(), dir, WaitOpts{Timeout: 5 * time.Second})
	if err != nil || res.State != StateDone || pass != 2 {
		t.Fatalf("must wait for second relay's done: pass=%d result=%+v err=%v", pass, res, err)
	}
	if s := LoadRegistryTolerant(dir).Slot("w1"); s.RelayPending() != 0 || s.RelayMarkerSeen() {
		t.Fatalf("completed relay metadata remains: %v", s.Raw())
	}
}

func TestPollRelayObservationSurvivesBusyCapture(t *testing.T) {
	dir, h := pollRegistry(t, "tmux")
	relayRaceSlot(t, dir, 1)
	e := envWith(h)
	h.panes["w1"] = []string{relayEcho(1), strings.Repeat("● work\n", 80)}
	res, err := e.Poll(bg, dir, PollOpts{Slot: "w1"})
	if err != nil || res.Slots[0].State != StateBusy {
		t.Fatalf("working after relay: %+v, %v", res, err)
	}
	if !LoadRegistryTolerant(dir).Slot("w1").RelayMarkerSeen() {
		t.Fatal("first-capture observation was not persisted while busy")
	}
	h.panes["w1"] = []string{"● finished\n", "ROTA-DONE w1\n"}
	res, err = e.Poll(bg, dir, PollOpts{Slot: "w1"})
	if err != nil || res.Slots[0].State != StateDone {
		t.Fatalf("later done without marker: %+v, %v", res, err)
	}
}

func TestPollRelayCompletionOnFinishedTurn(t *testing.T) {
	for _, done := range []string{"ROTA-DONE w1\n", "ROTA-DONE w1 https://github.com/o/r/pull/994\n"} {
		t.Run(strings.TrimSpace(done), func(t *testing.T) {
			dir, f := pollRegistry(t, "herdr")
			relayRaceSlot(t, dir, 1)
			if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetTurnSeq(40) }); err != nil {
				t.Fatal(err)
			}
			f.panes["w1"] = []string{relayEcho(1) + done}
			h := turnHost{f, map[string]host.Turn{"w1": {Status: "done", StateSeq: 52, CompletionSeq: 52}}}
			e := envWith(h)
			e.Sleep = func(time.Duration) { t.Fatal("finished turn should not settle") }
			res, err := e.Poll(bg, dir, PollOpts{Slot: "w1"})
			if err != nil || res.Slots[0].State != StateDone || countCalls(f.calls, "capture w1") != 1 {
				t.Fatalf("finished turn with visible relay: %+v, %v, calls=%v", res, err, f.calls)
			}
			if s := LoadRegistryTolerant(dir).Slot("w1"); s.State() != "done" || s.RelayPending() != 0 || s.RelayMarkerSeen() {
				t.Fatalf("finished turn did not clear relay: %v", s.Raw())
			}
		})
	}
}

func TestPollRelayReplacedDuringCaptureKeepsNewRelayPending(t *testing.T) {
	for _, replaceSession := range []bool{false, true} {
		t.Run(map[bool]string{false: "new relay", true: "new session"}[replaceSession], func(t *testing.T) {
			dir, h := pollRegistry(t, "tmux")
			relayRaceSlot(t, dir, 1)
			h.panes["w1"] = []string{relayEcho(1), relayEcho(1) + "ROTA-DONE w1\n"}
			e := envWith(h)
			e.Sleep = func(time.Duration) {
				if _, err := UpdateSlot(dir, "w1", func(s *Slot) {
					k := 2
					if replaceSession {
						s.Dispatch("new:session", "", "", "2026-11-03T10:00:00Z")
						k = 1 // numbering restarts with a new session
					}
					s.SetRelayPending(k, "2026-11-03T10:01:00Z")
				}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.Poll(bg, dir, PollOpts{Slot: "w1"}); err != nil {
				t.Fatal(err)
			}
			s := LoadRegistryTolerant(dir).Slot("w1")
			if s.State() != "busy" || s.RelayPending() == 0 || s.RelayMarkerSeen() {
				t.Fatalf("stale observation changed new relay: state=%s pending=%d seen=%v", s.State(), s.RelayPending(), s.RelayMarkerSeen())
			}
		})
	}
}
