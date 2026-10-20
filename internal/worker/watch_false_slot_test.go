package worker

import (
	"github.com/l4ci/rota/internal/stamp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// #1017: a brief that quotes the ROTA-DONE template is the orchestrator's text,
// whichever way the host echoes or wraps it.
func TestClassifyBriefQuotingDoneTemplateIsNotDone(t *testing.T) {
	quoted := "Final step: print exactly `ROTA-DONE nia <pr-url>` as the last line.\n" +
		"ROTA-DONE nia <pr-url>\n"
	wrapped := "Final step, after your PR summary: print exactly\nROTA-DONE nia <pr-url> (your PR's URL) as the last line"
	panes := map[string]string{
		"claude echo":     "❯ --- ORCHESTRATOR (round 27) ---\n  " + strings.ReplaceAll(quoted, "\n", "\n  "),
		"claude wrapped":  "❯ --- ORCHESTRATOR (round 27) ---\n  " + strings.ReplaceAll(wrapped, "\n", "\n  "),
		"bare echo":       "--- ORCHESTRATOR (round 27) ---\n" + quoted,
		"bare wrapped":    "--- ORCHESTRATOR (round 27) ---\n" + wrapped,
		"unindented tail": "❯ --- ORCHESTRATOR (round 27) ---\n  You are nia.\nROTA-DONE nia <pr-url>\n",
	}
	for name, pane := range panes {
		for _, kind := range []string{"claude", "codex", "opencode"} {
			if st, ev := ClassifyFor(kind, pane, false, 60, ""); st == StateDone {
				t.Errorf("%s/%s: brief template read as done: %q", name, kind, ev)
			}
		}
	}
}

// #1017: a Claude Code turn in flight (a running command or monitor) is busy
// even when the pane did not change between the two captures.
func TestClassifyClaudeRunningCommandIsNotIdle(t *testing.T) {
	for name, pane := range map[string]string{
		"esc to interrupt": "● Bash(until gh pr view; do sleep 30; done)\n  ⎿  Running…\n\n✻ Waiting… (3m 12s · esc to interrupt)\n",
		"running only":     "● Monitor(watch ci)\n  ⎿  Running…\n",
	} {
		if st, ev := ClassifyFor("claude", pane, false, 60, ""); st != StateBusy {
			t.Errorf("%s: %s %q, want busy", name, st, ev)
		}
	}
	// An echoed brief that mentions the phrase is not a running turn.
	echo := "❯ --- ORCHESTRATOR (round 27) ---\n  Press esc to interrupt a Running… command\n"
	if st, _ := ClassifyFor("claude", echo, false, 60, ""); st == StateBusy {
		t.Errorf("echoed brief read as busy")
	}
}

// relayEcho is a relay as the pane echoes it: header, note, its `[relay K]`
// line and a worker reply.
func relayEcho(k int) string {
	return "❯ --- ORCHESTRATOR (round 27) ---\n  [ORCHESTRATOR RELAY — forwarded.]\n  [relay " + strconv.Itoa(k) + "]\n\n  REVIEW\n\n● on it\n"
}

func relayRaceSlot(t *testing.T, dir string, k int) {
	t.Helper()
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) {
		s.Dispatch("w9:t1", "", "", "2026-10-20T10:00:00Z")
		for i := 0; i < k; i++ {
			s.AppendRelay(newRelayEntry(27, time.Now(), "REVIEW"))
		}
		s.SetRelayPending(k, stamp.Format(time.Now()))
	}); err != nil {
		t.Fatal(err)
	}
}

func waitOnce(t *testing.T, dir string, h *waitHost, d time.Duration) WaitResult {
	t.Helper()
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Timeout: d})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// #1017 AC-3: right after a dispatched relay the pane still shows only the old
// ROTA-DONE (the echo has not rendered): busy, no wake. Once the echo and the
// worker's new ROTA-DONE (same PR) are in, the done is news exactly once.
func TestWaitRelayRaceOldDoneIsBusyThenNewDoneWakesOnce(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	const done = "ROTA-DONE w1 https://github.com/o/r/pull/994\n"
	brief := "❯ --- ORCHESTRATOR (round 27) ---\n  You are w1.\n\n● working\n"
	relayRaceSlot(t, dir, 1)
	h.set("w1", brief+done, "idle")
	if res := waitOnce(t, dir, h, 300*time.Millisecond); !res.TimedOut {
		t.Fatalf("old done before the relay echo woke the watch: %+v", res)
	}
	h.set("w1", brief+done+"\n"+relayEcho(1)+done, "idle")
	if res := waitOnce(t, dir, h, 5*time.Second); res.Slot != "w1" || res.State != StateDone {
		t.Fatalf("new done after the relay: %+v", res)
	}
	if res := waitOnce(t, dir, h, 300*time.Millisecond); !res.TimedOut {
		t.Errorf("the same done woke twice: %+v", res)
	}
}

// Second relay: the pane holds [D0][R1][D1 same PR] and R2 is sent but not
// rendered. D1 answers R1, not R2: no wake until R2's echo and a done after it.
func TestWaitSecondRelayNotRenderedIsBusy(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	const done = "ROTA-DONE w1 https://github.com/o/r/pull/994\n"
	pane := "❯ --- ORCHESTRATOR (round 27) ---\n  You are w1.\n\n" + done + "\n" + relayEcho(1) + done
	relayRaceSlot(t, dir, 2)
	h.set("w1", pane, "idle")
	if res := waitOnce(t, dir, h, 300*time.Millisecond); !res.TimedOut {
		t.Fatalf("the first relay's done woke the watch for the second: %+v", res)
	}
	h.set("w1", pane+"\n"+relayEcho(2)+done, "idle")
	if res := waitOnce(t, dir, h, 5*time.Second); res.Slot != "w1" || res.State != StateDone {
		t.Fatalf("done after the second relay: %+v", res)
	}
}

// The old done scrolled out of the pane: once the relay's echo is visible, a
// done after it counts.
func TestWaitDoneAfterVisibleRelayCountsWithoutTheOldDone(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	relayRaceSlot(t, dir, 1)
	h.set("w1", relayEcho(1)+strings.Repeat("● work\n", 20)+"ROTA-DONE w1 https://github.com/o/r/pull/994\n", "idle")
	if res := waitOnce(t, dir, h, 5*time.Second); res.Slot != "w1" || res.State != StateDone {
		t.Fatalf("done after the visible relay echo: %+v", res)
	}
	if got := LoadRegistryTolerant(dir).Slot("w1").RelayPending(); got != 0 {
		t.Errorf("relayPending = %d after the done, want 0", got)
	}
}

// A done repeated with the PR url the last one carried is news after a relay.
func TestRowKeyOfADoneCountsRelays(t *testing.T) {
	r := PollRow{"w1", StateDone, "https://github.com/o/r/pull/994"}
	if rowKey(pollTarget{}, r) == rowKey(pollTarget{relays: 1}, r) {
		t.Error("a done after a relay shares the key of the one before it")
	}
}

// Evidence written in angle brackets is real; the template's is not.
func TestClassifyAngleBracketedURLIsEvidence(t *testing.T) {
	pane := "● ROTA-DONE nia <https://github.com/o/r/pull/9>\n"
	if st, ev := ClassifyFor("claude", pane, false, 60, ""); st != StateDone || ev != "<https://github.com/o/r/pull/9>" {
		t.Errorf("%s %q", st, ev)
	}
	blocked := "● ROTA-BLOCKED nia: <one question>\n"
	if st, _ := ClassifyFor("claude", blocked, false, 60, ""); st == StateBlocked {
		t.Error("the template's blocked line read as blocked")
	}
}

// A busy phrase quoted inside an echoed relay is the orchestrator's, not a
// turn in flight, on harnesses that gutter their echo as well.
func TestClassifyOpenCodeBusyPhraseInEchoedRelayIsNotBusy(t *testing.T) {
	pane := "┃ --- ORCHESTRATOR (round 27) ---\n┃ the footer says esc interrupt while a turn runs\n"
	if st, ev := ClassifyFor("opencode", pane, false, 60, ""); st == StateBusy {
		t.Errorf("echoed relay read as busy: %q", ev)
	}
	live := pane + "\nesc interrupt\n"
	if st, _ := ClassifyFor("opencode", live, false, 60, ""); st != StateBusy {
		t.Errorf("a running opencode turn read as %s", st)
	}
}

// The relay's echo was seen, then scrolled out of the capture behind a long
// fix: the done in the capture came after it and counts, once.
func TestWaitDoneAfterScrolledOutRelayEchoWakesOnce(t *testing.T) {
	for _, withEvent := range []bool{false, true} {
		dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
		h := newWaitHost("herdr")
		const done = "ROTA-DONE w1 https://github.com/o/r/pull/994\n"
		relayRaceSlot(t, dir, 1)
		// First capture: the echo is in, the worker is at work.
		h.set("w1", relayEcho(1)+"● working\n", "working")
		if res := waitOnce(t, dir, h, 300*time.Millisecond); !res.TimedOut {
			t.Fatalf("a working slot woke the watch: %+v", res)
		}
		if !LoadRegistryTolerant(dir).Slot("w1").RelayMarkerSeen() {
			t.Fatal("the visible echo was not recorded")
		}
		if withEvent {
			if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetEvent(EventDone, "https://github.com/o/r/pull/994", stamp.Format(time.Now())) }); err != nil {
				t.Fatal(err)
			}
		}
		// Later: 80 lines of work pushed the echo out; only the done shows.
		h.set("w1", strings.Repeat("● work\n", 80)+done, "idle")
		res := waitOnce(t, dir, h, 5*time.Second)
		if res.Slot != "w1" || res.State != StateDone {
			t.Fatalf("event=%v: done after a scrolled-out echo: %+v", withEvent, res)
		}
		if got := LoadRegistryTolerant(dir).Slot("w1").RelayPending(); got != 0 {
			t.Errorf("event=%v: relayPending = %d after the done", withEvent, got)
		}
		if res := waitOnce(t, dir, h, 300*time.Millisecond); !res.TimedOut {
			t.Errorf("event=%v: the same done woke twice: %+v", withEvent, res)
		}
	}
}

// A worker's done event after the relay is not held by an echo that never
// rendered, and the gated pane read does not clear it.
func TestWaitDoneEventPassesTheRelayGate(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	relayRaceSlot(t, dir, 1)
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetEvent(EventDone, "", stamp.Format(time.Now().Add(time.Second))) }); err != nil {
		t.Fatal(err)
	}
	h.set("w1", "ROTA-DONE w1 https://github.com/o/r/pull/994\n", "idle")
	if res := waitOnce(t, dir, h, 5*time.Second); res.Slot != "w1" || res.State != StateDone {
		t.Fatalf("done event after the relay: %+v", res)
	}
}

// A relay whose marker never shows (a collapsed paste) is given up on after
// round.stallMinutes instead of holding the slot busy for good.
func TestNoteRelayEchoGivesUpAfterStallMinutes(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	relayRaceSlot(t, dir, 1)
	targets := []pollTarget{slotTarget(LoadRegistryTolerant(dir).Slot("w1"))}
	noteRelayEcho(dir, targets, time.Now().Add(time.Minute))
	if LoadRegistryTolerant(dir).Slot("w1").RelayPending() != 1 {
		t.Fatal("a young relay was given up on")
	}
	noteRelayEcho(dir, targets, time.Now().Add(31*time.Minute))
	if got := LoadRegistryTolerant(dir).Slot("w1").RelayPending(); got != 0 || targets[0].relayPending != 0 {
		t.Errorf("relayPending = %d (target %d) after the stall window", got, targets[0].relayPending)
	}
}

// With round.stallMinutes=0 an unseen relay is still given up on, after the
// fixed fallback window.
func TestNoteRelayEchoStallOffUsesFallbackWindow(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"round":{"stallMinutes":0}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	relayRaceSlot(t, dir, 1)
	targets := []pollTarget{slotTarget(LoadRegistryTolerant(dir).Slot("w1"))}
	noteRelayEcho(dir, targets, time.Now().Add(time.Minute))
	if LoadRegistryTolerant(dir).Slot("w1").RelayPending() != 1 {
		t.Fatal("a young relay was given up on")
	}
	noteRelayEcho(dir, targets, time.Now().Add(relayFallbackWindow+time.Minute))
	if got := LoadRegistryTolerant(dir).Slot("w1").RelayPending(); got != 0 || targets[0].relayPending != 0 {
		t.Errorf("relayPending = %d (target %d) after the fallback window", got, targets[0].relayPending)
	}
}

func TestClassifyCodexBusyPhraseInEchoedRelayIsNotBusy(t *testing.T) {
	pane := "❯ --- ORCHESTRATOR (round 27) ---\n  the footer says (esc to interrupt) while a turn runs\n"
	if st, ev := ClassifyFor("codex", pane, false, 60, ""); st == StateBusy {
		t.Errorf("echoed relay read as busy: %q", ev)
	}
	if st, _ := ClassifyFor("claude", "✻ Working… (12s · esc to interrupt · ctrl+t to hide)\n", false, 60, ""); st != StateBusy {
		t.Errorf("a claude status line with trailing hints read as %s", st)
	}
}

// #1026: a done event stamped in the same second as the relay, but sent before
// it, is the earlier turn's: only an event newer than the relay passes.
func TestDoneEventAfterRelayIgnoresSameSecondStaleEvent(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	now := stamp.Format(time.Now())
	after := func() (ok bool) {
		if _, err := UpdateSlot(dir, "w1", func(s *Slot) { ok = doneEventAfterRelay(slotTarget(s)) }); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) {
		s.SetEvent(EventDone, "", now)
		s.SetRelayPending(1, now)
	}); err != nil {
		t.Fatal(err)
	}
	if after() {
		t.Fatal("same-second done event sent before the relay passed the gate")
	}
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.SetEvent(EventDone, "", now) }); err != nil {
		t.Fatal(err)
	}
	if !after() {
		t.Fatal("same-second done event sent after the relay was refused")
	}
}

// #1156: a footer that counts the session's own background shells or monitors
// is busy on a still pane, with evidence that does not carry the count.
func TestClassifyClaudeBackgroundTasksAreBusy(t *testing.T) {
	foot := func(n string) string {
		return "● Bash(rota test run fast)\n\n  ⏵⏵ bypass permissions on · " + n + " (shift+tab to cycle)\n"
	}
	var first string
	for _, n := range []string{"1 shell", "1 shell, 2 monitors", "3 monitors"} {
		st, ev := ClassifyFor("claude", foot(n), false, 60, "")
		if st != StateBusy {
			t.Errorf("%q: %s %q, want busy", n, st, ev)
		}
		if first == "" {
			first = ev
		} else if ev != first {
			t.Errorf("%q: evidence %q changed from %q", n, ev, first)
		}
	}
	// No background tasks, no sentinel: idle as before.
	idle := "● done thinking\n\n  ⏵⏵ bypass permissions on (shift+tab to cycle)\n"
	if st, _ := ClassifyFor("claude", idle, false, 60, ""); st != StateIdle {
		t.Errorf("footer without tasks: %s, want idle", st)
	}
}

// #1156 review: the task footer stays on screen at a dialog or after a crash;
// it must not mask those states, and "0 shells" is not a running task.
func TestClassifyBackgroundFooterKeepsStalledStates(t *testing.T) {
	foot := "\n  ⏵⏵ bypass permissions on · 1 shell, 2 monitors\n"
	for name, c := range map[string]struct {
		pane, native, want string
	}{
		"blocked":    {"● x\n" + foot, "blocked", StateNeedsPermission},
		"gone":       {"● x\n" + foot, "gone", StateDead},
		"api error":  {"● x\n  ⎿  API Error: 500 internal\n" + foot, "", StateDead},
		"permission": {"Do you want to proceed?\n ❯ 1. Yes\n   2. No\n" + foot, "", StateNeedsPermission},
	} {
		base, _ := ClassifyFor("claude", strings.Replace(c.pane, foot, "\n", 1), false, 60, c.native)
		if st, ev := ClassifyFor("claude", c.pane, false, 60, c.native); st != base || st != c.want {
			t.Errorf("%s: %s %q, want %s (without footer %s)", name, st, ev, c.want, base)
		}
	}
	zero := "● x\n\n  ⏵⏵ bypass permissions on · 0 shells\n"
	if st, _ := ClassifyFor("claude", zero, false, 60, ""); st != StateIdle {
		t.Errorf("0 shells read as %s, want idle", st)
	}
}
