package harness

import (
	"fmt"
	"regexp"
	"strings"
)

// Every brief and relay rota sends a worker sits inside one element:
//
//	<rota-relay n="3" round="12" from="orchestrator">
//	…text…
//	</rota-relay>
//
// n is the relay's sequence number in the slot's log and is absent from a task
// brief. The opening line is also the marker relay detection matches on, so it
// stays a line of its own at the top of the payload: a busy agent's queue
// preview shows only the first lines. The closing line is where the
// orchestrator's text ends. herdr-core may wrap the send in its own
// `<hc from="tN">…</hc>`; rota's element sits inside that.
const (
	// RelayTag is the element's name.
	RelayTag = "rota-relay"
	// RelayClose is the closing line.
	RelayClose = "</" + RelayTag + ">"
)

// RelayOpen is the opening line for a payload sent in round. seq numbers a
// relay; a task brief passes 0 and carries no n.
func RelayOpen(round, seq int) string {
	if seq > 0 {
		return fmt.Sprintf(`<%s n="%d" round="%d" from="orchestrator">`, RelayTag, seq, round)
	}
	return fmt.Sprintf(`<%s round="%d" from="orchestrator">`, RelayTag, round)
}

// WrapRelay is text inside its opening and closing line, ending in a newline.
func WrapRelay(round, seq int, text string) string {
	return RelayOpen(round, seq) + "\n" + strings.TrimRight(text, "\n") + "\n" + RelayClose + "\n"
}

// linePrefix is what a terminal puts before a line: indentation and an echo or
// gutter glyph (Claude Code "❯", OpenCode "┃", Codex "›", and "↳" in a queue
// preview).
const linePrefix = `^\s*(?:[┃❯›↳]\s*)*`

var (
	// relayOpenRe matches an opening line, with or without n. A herdr-core
	// `<hc from="tN">` ahead of it on the same line is allowed.
	relayOpenRe = regexp.MustCompile(linePrefix + `(?:<hc\b[^>]*>\s*)?<` + RelayTag + `(?: n="(\d+)")? round="\d+" from="orchestrator">\s*$`)
	// relayCloseRe matches the closing line, a herdr-core `</hc>` after it
	// included.
	relayCloseRe = regexp.MustCompile(linePrefix + `</` + RelayTag + `>\s*(?:</hc>\s*)?$`)
	// relaySequence matches the legacy `[relay K]` line beneath a relay header,
	// still found in panes a relay sent before the element existed left behind.
	relaySequence = regexp.MustCompile(linePrefix + `\[relay (\d+)\]\s*$`)
)

// IsRelayOpen reports an opening line of the element, behind any gutter.
func IsRelayOpen(line string) bool { return relayOpenRe.MatchString(line) }

// IsRelayClose reports a closing line of the element, behind any gutter.
func IsRelayClose(line string) bool { return relayCloseRe.MatchString(line) }

// RelaySequence returns the sequence a relay's opening line carries (or the
// legacy `[relay K]` line), or "" for any other line, a task brief's opening
// line included. A marker quoted within prose is not a relay echo.
func RelaySequence(line string) string {
	if m := relayOpenRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	if m := relaySequence.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}
