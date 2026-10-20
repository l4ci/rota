package worker

import (
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

// #1017 AC-3: right after a dispatched relay the pane still shows only the old
// ROTA-DONE (the echo has not rendered): busy, no wake. Once the echo and the
// worker's new ROTA-DONE (same PR) are in, the done is news exactly once.
func TestWaitRelayRaceOldDoneIsBusyThenNewDoneWakesOnce(t *testing.T) {
	dir := waitProject(t, 1, map[string]string{"w1": "w9:t1"})
	h := newWaitHost("herdr")
	const done = "ROTA-DONE w1 https://github.com/o/r/pull/994\n"
	brief := "❯ --- ORCHESTRATOR (round 27) ---\n  You are w1.\n\n● working\n"
	// The slot was done and reported; a relay then went out through dispatch.
	if _, err := UpdateSlot(dir, "w1", func(s *Slot) {
		s.Dispatch("w9:t1", "", "", "2026-10-20T10:00:00Z")
		s.AppendRelay(newRelayEntry(27, time.Now(), "REVIEW"))
		s.SetRelayPending()
	}); err != nil {
		t.Fatal(err)
	}
	h.set("w1", brief+done, "idle")
	res, err := envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Timeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("old done before the relay echo woke the watch: %+v", res)
	}
	h.set("w1", brief+done+"\n❯ --- ORCHESTRATOR (round 27) ---\n  REVIEW\n\n● fixed\n"+done, "idle")
	res, err = envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if res.Slot != "w1" || res.State != StateDone {
		t.Fatalf("new done after the relay: %+v", res)
	}
	res, err = envWith(watcherHost{h}).Wait(bg, dir, WaitOpts{Timeout: 300 * time.Millisecond})
	if err != nil || !res.TimedOut {
		t.Errorf("the same done woke twice: %+v %v", res, err)
	}
}

// A done repeated with the PR url the last one carried is news after a relay.
func TestRowKeyOfADoneCountsRelays(t *testing.T) {
	r := PollRow{"w1", StateDone, "https://github.com/o/r/pull/994"}
	if rowKey(pollTarget{}, r) == rowKey(pollTarget{relays: 1}, r) {
		t.Error("a done after a relay shares the key of the one before it")
	}
}
