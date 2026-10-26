package worker

import (
	"fmt"
	"strings"
)

// MaxSummaryLine caps one summary line: the three ride one event body.
const MaxSummaryLine = 200

// ParseSummary reads the three summary lines of a typed outcome (what changed,
// what was found, what remains) from text. Blank lines are dropped; a line
// holding " | " (the event's join) or over MaxSummaryLine characters is
// refused, as is any count but three.
func ParseSummary(text string) ([]string, error) {
	var summary []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			summary = append(summary, l)
		}
	}
	for _, l := range summary {
		if strings.Contains(l, " | ") {
			return nil, fmt.Errorf("--summary-file lines must not contain \" | \" (it joins them in the event): %q", l)
		}
		if n := len([]rune(l)); n > MaxSummaryLine {
			return nil, fmt.Errorf("--summary-file lines are at most %d characters, got %d", MaxSummaryLine, n)
		}
	}
	if len(summary) != 3 {
		return nil, fmt.Errorf("--summary-file must hold exactly three lines (what changed, what was found, what remains), got %d", len(summary))
	}
	return summary, nil
}

// OutcomeEvidence is the done event's body: the PR, as before, or for a typed
// outcome "<outcome>: <line> | <line> | <line>", which `round wait` and
// `round status` show beside the slot state.
func OutcomeEvidence(kind string, summary []string, pr string) string {
	if kind == "" {
		return pr
	}
	if len(summary) == 0 {
		return kind
	}
	return kind + ": " + strings.Join(summary, " | ")
}
