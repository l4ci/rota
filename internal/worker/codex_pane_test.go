package worker

import (
	"os"
	"testing"

	"github.com/l4ci/rota/internal/harness"
)

func codexPaneFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/panes/codex-" + name + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestClassifyCodexTerminalWait(t *testing.T) {
	pane := codexPaneFixture(t, "terminal-wait")
	for _, native := range []string{"", "idle", "blocked", "unknown"} {
		t.Run(native, func(t *testing.T) {
			if state, evidence := ClassifyFor(harness.Codex, pane, false, 60, native); state != StateBusy {
				t.Fatalf("terminal wait: %s %q, want BUSY", state, evidence)
			}
		})
	}
}

func TestClassifyCodexTerminalWaitKeepsAttentionStates(t *testing.T) {
	pane := codexPaneFixture(t, "terminal-wait")
	for name, c := range map[string]struct {
		tail, native, want string
	}{
		"blocked sentinel":      {"\n• ROTA-BLOCKED dana: which base should I use?\n", "blocked", StateBlocked},
		"done sentinel":         {"\n• ROTA-DONE dana https://github.com/o/r/pull/9\n", "blocked", StateDone},
		"approval":              {"\nDo you want to proceed?\n ❯ 1. Yes\n   2. No\n", "blocked", StateNeedsPermission},
		"approval without host": {"\nDo you want to proceed?\n ❯ 1. Yes\n   2. No\n", "", StateNeedsPermission},
		"gone":                  {"", "gone", StateDead},
		"api failure":           {"\nAPI Error: 500 internal\n", "blocked", StateDead},
	} {
		t.Run(name, func(t *testing.T) {
			if state, evidence := ClassifyFor(harness.Codex, pane+c.tail, false, 60, c.native); state != c.want {
				t.Fatalf("%s %q, want %s", state, evidence, c.want)
			}
		})
	}
}

func TestClassifyCodexTerminalWaitIgnoresQuotesAndOtherHarnesses(t *testing.T) {
	pane := codexPaneFixture(t, "terminal-wait")
	for _, kind := range []string{"", harness.Claude, harness.OpenCode} {
		if state, evidence := ClassifyFor(kind, pane, false, 60, "blocked"); state != StateNeedsPermission {
			t.Errorf("%q: %s %q, want NEEDS-PERMISSION", kind, state, evidence)
		}
	}
	for _, quote := range []string{
		"• Ran cat fixture.txt\n  └ • Waiting for background terminal · sleep 45\n",
		"• The display says Waiting for background terminal while waiting.\n",
		"• Waiting for background terminal is normal during a test run.\n",
		"--- ORCHESTRATOR (round 40) ---\nThe pane says Waiting for background terminal.\n--- ROTA-SIG abcdef ---\n",
		"• Waited for background terminal · sleep 45\n",
	} {
		if state, evidence := ClassifyFor(harness.Codex, quote, false, 60, "blocked"); state != StateNeedsPermission {
			t.Errorf("quoted/completed wait: %s %q, want NEEDS-PERMISSION", state, evidence)
		}
	}
}

// These three panes were captured from Codex 0.164.0 through herdr. Both
// poll/watch and round status must distinguish a tool wait from real input.
func TestClassifyCodexCapturedAttentionPanes(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"terminal-wait", StateBusy},
		{"blocked", StateBlocked},
		{"approval", StateNeedsPermission},
	} {
		t.Run(c.name, func(t *testing.T) {
			if state, evidence := ClassifyFor(harness.Codex, codexPaneFixture(t, c.name), false, 60, "blocked"); state != c.want {
				t.Fatalf("%s %q, want %s", state, evidence, c.want)
			}
		})
	}
	approval := codexPaneFixture(t, "approval")
	for _, native := range []string{"", "blocked"} {
		if state, evidence := ClassifyFor(harness.Codex, codexPaneFixture(t, "terminal-wait")+approval, false, 60, native); state != StateNeedsPermission {
			t.Errorf("wait plus approval: %s %q, want NEEDS-PERMISSION", state, evidence)
		}
	}
}

func TestPollCodexCapturedPanes(t *testing.T) {
	for _, c := range []struct{ fixture, want, recorded string }{
		{"terminal-wait", StateBusy, "busy"},
		{"blocked", StateBlocked, "blocked"},
		{"approval", StateNeedsPermission, "needs-permission"},
	} {
		t.Run(c.fixture, func(t *testing.T) {
			dir, h := pollRegistry(t, "herdr")
			if _, err := UpdateSlot(dir, "w1", func(s *Slot) { s.Raw().Set("kind", harness.Codex) }); err != nil {
				t.Fatal(err)
			}
			pane := codexPaneFixture(t, c.fixture)
			h.panes["w1"] = []string{pane, pane}
			h.status["w1"] = "blocked"
			res, err := envWith(h).Poll(bg, dir, PollOpts{Slot: "w1", Lines: 60})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Slots) != 1 || res.Slots[0].State != c.want {
				t.Fatalf("poll: %+v, want %s", res, c.want)
			}
			if got := slotField(t, dir, "w1", "state"); got != c.recorded {
				t.Errorf("recorded state %q, want %q", got, c.recorded)
			}
		})
	}
}
