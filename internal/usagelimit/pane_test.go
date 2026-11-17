package usagelimit

import (
	"regexp"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/harness"
)

func TestLimitRegexIsTheAlternationOfTheClassifierPhrases(t *testing.T) {
	re := regexp.MustCompile(LimitRegex())
	if len(limitPatterns) != len(limitPhrases) {
		t.Fatalf("%d patterns for %d phrases", len(limitPatterns), len(limitPhrases))
	}
	for _, msg := range []string{"You've reached your usage limit", "Claude USAGE LIMIT REACHED", "Youve hit your usage limit", "Your limit will reset at 3pm", "Approaching your usage limit"} {
		if !re.MatchString(msg) {
			t.Errorf("%q not matched", msg)
		}
		if _, _, ok := FindLimit("", msg); ok == strings.HasPrefix(msg, "Approaching") {
			t.Errorf("%q: FindLimit ok=%v", msg, ok)
		}
	}
	if re.MatchString("the limit of the buffer") {
		t.Error("matched a bare limit")
	}
}

// FindLimit reads a kind's own phrases on top of the shared ones, for that kind only.
func TestFindLimitReadsTheKindsOwnPhrases(t *testing.T) {
	const quota = "Error: insufficient quota\n"
	if _, _, ok := FindLimit(harness.OpenCode, quota); !ok {
		t.Error("opencode quota text is not a limit for an opencode slot")
	}
	for _, kind := range []string{"", harness.Claude, harness.Codex} {
		if _, _, ok := FindLimit(kind, quota); ok {
			t.Errorf("opencode quota text is a limit for kind %q", kind)
		}
	}
}

// The herdr prefilter must let a kind's own limit text through, or the
// watcher's per-kind reading never sees it.
func TestLimitRegexIncludesKindPhrases(t *testing.T) {
	if !regexp.MustCompile(LimitRegex()).MatchString("Error: insufficient quota") {
		t.Error("LimitRegex drops opencode quota text")
	}
}

// A reply-marker block's gutter lines are dialog only when no indented
// paragraph of the same reply follows it: the paragraph proves the "⎿" output
// belongs to the reply, so the limit phrase under it is not a hit limit.
func TestDialogRegionGutterBeforeIndentedParagraphIsReply(t *testing.T) {
	footer := "\n❯ \n  ⏵⏵ bypass permissions on\n"
	const phrase = "You've hit your usage limit"
	with := "● Update(x)\n  ⎿  " + phrase + "\n\n  The edit is done.\n" + footer
	if got := DialogRegion(with, nil); strings.Contains(got, phrase) {
		t.Errorf("gutter line before a reply paragraph kept as dialog: %q", got)
	}
	without := "● Update(x)\n  ⎿  " + phrase + "\n" + footer
	if got := DialogRegion(without, nil); !strings.Contains(got, phrase) {
		t.Errorf("idle gutter line with no paragraph after it dropped: %q", got)
	}
}

// The working flag is seeded from the status area under the input box's edge:
// a spinner or busy footer only there still marks the pane working, so "⎿"
// tool output quoting the phrase above the box is not a hit limit.
func TestDialogRegionWorkingFromStatusAreaOnly(t *testing.T) {
	const phrase = "You've hit your usage limit"
	reply := "● Bash(grep limit poll.go)\n  ⎿  poll.go:417: " + phrase + "\n"
	box := "\n╭────╮\n│ ❯  │\n╰────╯\n"
	got := DialogRegion(reply+box+"✻ Running… (3s · esc to interrupt)\n", nil)
	if strings.Contains(got, phrase) {
		t.Errorf("spinner below the box edge not read as working: %q", got)
	}
	idle := DialogRegion(reply+box+"  opus · ctx 40%\n", nil)
	if !strings.Contains(idle, phrase) {
		t.Errorf("idle pane dropped its gutter line: %q", idle)
	}
}

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
