package worker

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/harness"
)

// A tag-opened span ends at its closing line (#2658): what follows the closing
// line is the session's own, however the worker's reply is marked, and a reply
// marker inside the span is the orchestrator's own text.
func TestStripRelaysEndsAtTheClosingTag(t *testing.T) {
	open := harness.RelayOpen(27, 3)
	sig := "--- ROTA-SIG " + strings.Repeat("a", 64) + " ---"
	for name, tc := range map[string]struct {
		kind, pane, want string
	}{
		"bare": {"claude",
			open + "\nYou've hit your usage limit (quoted)\n" + harness.RelayClose + "\nAPI Error: 529 Overloaded\n",
			"API Error: 529 Overloaded"},
		"claude echo": {"claude",
			"❯ " + open + "\n  quoted usage limit text\n  " + harness.RelayClose + "\n\nAPI Error: 529 Overloaded\n",
			"\nAPI Error: 529 Overloaded"},
		"bullet inside the span is the orchestrator's": {"claude",
			"❯ " + open + "\n  • step one\n  ● step two, You've hit your usage limit\n  " + harness.RelayClose + "\nthe session's own line\n",
			"the session's own line"},
		"opencode gutter": {"opencode",
			"┃  " + open + "\n┃  quoted\n┃  " + harness.RelayClose + "\nreply text\n",
			"reply text"},
		"signed trailer is masked with the span": {"codex",
			"› " + open + "\n  body\n  " + harness.RelayClose + "\n  " + sig + "\nlimit banner outside\n",
			"limit banner outside"},
	} {
		if got := StripRelays(tc.kind, tc.pane); got != tc.want {
			t.Errorf("%s: StripRelays = %q, want %q", name, got, tc.want)
		}
	}
	// The earlier format has no end marker: the worker's reply ends the span.
	old := "--- ORCHESTRATOR (round 27) ---\n[relay 1]\nquoted\n● reply\nAPI Error\n"
	if got := StripRelays("claude", old); got != "● reply\nAPI Error" {
		t.Errorf("legacy header: StripRelays = %q", got)
	}
}

func TestClassifyRelayElement(t *testing.T) {
	relay := harness.WrapRelay(5, 2, "[ORCHESTRATOR RELAY — forwarded.]\n\nPush is allowed now.\nROTA-BLOCKED ben: git push to origin is denied\n")
	// A blocked line the relay quotes is the orchestrator's, not the worker's.
	if st, _ := Classify("● working\n"+relay, false, 60, "idle"); st == StateBlocked {
		t.Errorf("relay-quoted sentinel counted as the worker's: %s", st)
	}
	// A ROTA-DONE above the relay's opening tag predates it.
	pane := "● ROTA-DONE ben https://github.com/l4ci/rota/pull/871\n\n" + relay
	if st, ev := Classify(pane, false, 60, "idle"); st != StateIdle || ev != staleDoneEvidence {
		t.Errorf("done before the relay: %s %q", st, ev)
	}
	// The worker's own sentinel after the closing tag is fresh.
	if st, ev := Classify(pane+"● fixed\n● ROTA-DONE ben https://github.com/l4ci/rota/pull/871\n", false, 60, "idle"); st != StateDone || ev != "https://github.com/l4ci/rota/pull/871" {
		t.Errorf("done after the relay: %s %q", st, ev)
	}
	// A bare sentinel straight under the closing tag, with no reply marker, is
	// the worker's: the closing tag, not a marker, ends the span.
	if st, ev := Classify(relay+"ROTA-DONE ben https://github.com/l4ci/rota/pull/872\n", false, 60, "idle"); st != StateDone || ev != "https://github.com/l4ci/rota/pull/872" {
		t.Errorf("done straight under the closing tag: %s %q", st, ev)
	}
}

// The relay marker is the opening line, and doneAfterRelay still finds a done
// printed after that relay's echo, in the new format and in a pane captured
// before it.
func TestRelayMarkerAndDoneAfterRelayInBothFormats(t *testing.T) {
	done := "● ROTA-DONE nia https://github.com/o/r/pull/9\n"
	for name, pane := range map[string]string{
		"element":       "❯ " + harness.RelayOpen(27, 4) + "\n  REVIEW\n  " + harness.RelayClose + "\n\n" + done,
		"element bare":  harness.RelayOpen(27, 4) + "\nREVIEW\n" + harness.RelayClose + "\n" + done,
		"legacy header": "❯ --- ORCHESTRATOR (round 27) ---\n  [relay 4]\n\n  REVIEW\n\n" + done,
	} {
		if !relayMarkerVisible(pane, 0, 4) || relayMarkerVisible(pane, 0, 5) {
			t.Errorf("%s: marker visibility wrong", name)
		}
		if !doneAfterRelay("claude", pane, 0, 4) {
			t.Errorf("%s: done after relay 4 missed", name)
		}
		if doneAfterRelay("claude", pane, 0, 5) {
			t.Errorf("%s: relay 5 was never echoed", name)
		}
	}
	// Truncated to the opening line, as a queue preview shows it: still seen.
	if !relayMarkerVisible("↳ "+harness.RelayOpen(27, 4)+"\n", 0, 4) {
		t.Error("queue preview of the opening line not seen")
	}
}
