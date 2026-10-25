package harness

import "regexp"

// relaySequence matches the marker dispatch puts beneath each relay header,
// including indentation and the gutters used by terminal harnesses.
var relaySequence = regexp.MustCompile(`^\s*(?:[┃❯]\s*)?\[relay (\d+)\]\s*$`)

// RelaySequence returns the sequence written on a relay marker line, or ""
// for any other line. A marker quoted within prose is not a relay echo.
func RelaySequence(line string) string {
	if m := relaySequence.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}
