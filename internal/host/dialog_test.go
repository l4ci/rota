package host

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/harness"
)

func TestDialogKeys(t *testing.T) {
	trust := claudeTrustScreen(t)
	bypass := " WARNING: Bypass Permissions mode\n   1. No, exit\n ❯ 2. Yes, I accept\n"
	cursorOnNo := "   1. Yes, proceed\n ❯ 2. No\n"
	noCursor := "   1. Yes, proceed\n   2. No, exit\n"
	cases := []struct {
		name, pane string
		want       []string
		ok         bool
	}{
		{"trust", trust, []string{"down", "enter"}, true},
		{"bypass", bypass, []string{"enter"}, true},
		{"accept above cursor", cursorOnNo, []string{"up", "enter"}, true},
		{"accept below cursor", "❯ 1. No, exit\n  2. Other\n  3. Yes, I accept\n", []string{"down", "down", "enter"}, true},
		{"no cursor defaults to 1", noCursor, []string{"enter"}, true},
		// Both startup dialogs initially select "No, exit".
		{"bypass cursor on No, exit", "WARNING: Claude Code running in Bypass Permissions mode\n ❯ 1. No, exit\n   2. Yes, I accept\n", []string{"down", "enter"}, true},
		{"unknown dialog", "Pick a colour\n ❯ 1. Red\n   2. Blue\n", nil, false},
		{"unknown", "1. Something else\n2. Other\n", nil, false},
		// Claude Code v2.1.288 dropped the numbers (#209).
		{"unnumbered trust", " ❯ No, exit\n   Yes, I trust this folder\n\n Enter to confirm · Esc to cancel\n", []string{"down", "enter"}, true},
		{"unnumbered, cursor on yes", " Quick safety check\n\n   No, exit\n ❯ Yes, I trust this folder\n", []string{"enter"}, true},
		{"unnumbered unknown", " ❯ Red\n   Blue\n", nil, false},
		{"empty", "", nil, false},
		// Narrowing pins (#3640): the accepter never widens past the old grammar.
		{"paren-numbered block, no cursor", "   1) Yes, proceed\n   2) No, exit\n", nil, false},
		{"theme cursor on Light mode", "   1. Dark mode\n ❯ 2. Light mode\n", nil, false},
		{"Dark modern is not a theme", " ❯ Dark modern\n   Light mode\n", nil, false},
		{"Light modern is not a theme", " ❯ Dark mode\n   Light modern\n", nil, false},
	}
	for _, c := range cases {
		got, ok := DialogKeys(c.pane)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: DialogKeys = %v %v, want %v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestDialogKeysRealTrustDialog pins the folder-trust dialog exactly as
// Claude Code v2.1.288 drew it under herdr 0.9.3 (captured in round 4's live
// check, #209): unnumbered options, cursor on "No, exit".
func TestDialogKeysRealTrustDialog(t *testing.T) {
	b, err := os.ReadFile("testdata/trust-dialog-2.1.288.txt")
	if err != nil {
		t.Fatal(err)
	}
	if keys, ok := DialogKeys(string(b)); !ok || !reflect.DeepEqual(keys, []string{"down", "enter"}) {
		t.Errorf("DialogKeys = %v %v, want [down enter] true", keys, ok)
	}
}

func TestTrustDialogFixtureMatchesRecoveryHint(t *testing.T) {
	pane := readFixture(t, "trust-dialog-2.1.288.txt")
	hint := harness.StartupDialogRecovery(harness.Claude, "t2")
	for _, text := range []string{harness.ClaudeTrustDecline, harness.ClaudeTrustAccept} {
		found := false
		for _, line := range strings.Split(pane, "\n") {
			_, _, row, ok := option(line)
			if ok && row == text {
				found = true
			}
		}
		if !found {
			t.Errorf("captured trust dialog has no option exactly %q", text)
		}
		if !strings.Contains(hint, `"`+text+`"`) {
			t.Errorf("recovery hint %q does not quote option %q", hint, text)
		}
	}
}

// The Claude first-run theme picker is a startup dialog to drive through
// (Enter keeps the highlighted default), not an unknown screen (#1777).
func TestDialogKeysOnboardingThemePicker(t *testing.T) {
	pane := readFixture(t, "claude-onboarding-theme-2.1.290.txt")
	if keys, ok := DialogKeys(pane); !ok || !reflect.DeepEqual(keys, []string{"enter"}) {
		t.Errorf("DialogKeys = %v %v, want [enter] true", keys, ok)
	}
	numbered := "Choose the text style that looks best with your terminal\n\n ❯ 1. Dark mode ✔\n   2. Light mode\n"
	if keys, ok := DialogKeys(numbered); !ok || !reflect.DeepEqual(keys, []string{"enter"}) {
		t.Errorf("numbered: DialogKeys = %v %v, want [enter] true", keys, ok)
	}
}

func TestReadDialogRecognisesAutocompactPicker(t *testing.T) {
	d, ok := ReadDialog("⏺ done\n\n" + autocompactPicker(t))
	if !ok {
		t.Fatal("autocompact picker not recognised as a dialog")
	}
	if d.First != "We recommend compacting earlier" {
		t.Errorf("First = %q", d.First)
	}
	if len(d.Dismiss) != 1 || d.Dismiss[0] != "esc" {
		t.Errorf("Dismiss = %v, want [esc]", d.Dismiss)
	}
	if humanDraft(autocompactPicker(t), "brief") != "" {
		t.Error("picker row read as a human draft")
	}
}

func TestReadDialogUnknownHasNoDismissal(t *testing.T) {
	pane := "──────────────\n Update available\n\n ❯ Install now\n   Later\n\n Enter to select · Esc to cancel\n"
	d, ok := ReadDialog(pane)
	if !ok || d.First != "Update available" || d.Dismiss != nil {
		t.Errorf("ReadDialog = %+v, %v", d, ok)
	}
	if _, ok := ReadDialog("⏺ done\n❯ half a senten\n"); ok {
		t.Error("typed draft read as a dialog")
	}
}

// The theme and autocompact fixtures go through the one grammar (the trust
// dialog is pinned by TestDialogKeysRealTrustDialog): the theme dialog is
// accepted by DialogKeys, the autocompact picker is read but never accepted.
func TestDialogFixturesCrossOneGrammar(t *testing.T) {
	for _, c := range []struct {
		file    string
		keys    []string
		accepts bool
	}{
		{"claude-onboarding-theme-2.1.290.txt", []string{"enter"}, true},
		{"autocompact-picker-2.1.290.txt", nil, false},
	} {
		pane := readFixture(t, c.file)
		keys, ok := DialogKeys(pane)
		if ok != c.accepts || !reflect.DeepEqual(keys, c.keys) {
			t.Errorf("%s: DialogKeys = %v %v, want %v %v", c.file, keys, ok, c.keys, c.accepts)
		}
	}
}

// A numbered block on the prompt line reads as a dialog with `)` (as before)
// but a `> 1.` row is a typed quote, not a dialog (#3640).
func TestReadDialogNumberedRows(t *testing.T) {
	if _, ok := ReadDialog("⏺ done\n❯ 1) Red\n  2) Blue\n"); !ok {
		t.Error("1) row not read as a dialog")
	}
	if _, ok := ReadDialog("⏺ done\n❯ > 1. Red\n"); ok {
		t.Error("> 1. row read as a dialog")
	}
}
