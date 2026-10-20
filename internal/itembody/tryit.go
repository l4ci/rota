package itembody

import (
	"fmt"
	"regexp"
	"strings"
)

// TryItHeadRe matches a "Try it" heading: the commands a user would run to
// see the item work, which the merge gate runs on the merged tree (#899).
var TryItHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+try[ \t]+it[ \t]*$`)

// TryStep is one step of an item's "Try it" section: a command, the
// substrings its output must contain, and the body line it starts on.
type TryStep struct {
	Run    string
	Expect []string
	Line   int
}

// ParseTryIt reads the "Try it" section of body. Each "- " or "* " bullet is a
// step, its command with one pair of surrounding backticks trimmed; each
// `expect: <substring>` line after it (bare or as a nested bullet) adds a
// substring the step's output must contain. Other lines are prose. ok is false
// when the body has no such section; an expect line before any step, or one
// with no text, is an error naming its line, since a check the author thinks
// is there must not silently vanish.
func ParseTryIt(body string) (steps []TryStep, ok bool, err error) {
	loc := TryItHeadRe.FindStringIndex(body)
	if loc == nil {
		return nil, false, nil
	}
	sec, _ := Section(body, TryItHeadRe)
	first := strings.Count(body[:loc[1]], "\n") + 1 // the line the section text starts on
	for i, raw := range strings.Split(sec, "\n") {
		line := strings.TrimSpace(raw)
		bullet := strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ")
		text := line
		if bullet {
			text = strings.TrimSpace(line[2:])
		}
		if rest, isExpect := strings.CutPrefix(text, "expect:"); isExpect {
			sub := unquote(rest)
			switch {
			case len(steps) == 0:
				return nil, true, fmt.Errorf("Try it line %d: expect: comes before any step", first+i)
			case sub == "":
				return nil, true, fmt.Errorf("Try it line %d: expect: has no text", first+i)
			}
			steps[len(steps)-1].Expect = append(steps[len(steps)-1].Expect, sub)
			continue
		}
		if !bullet {
			continue
		}
		if run := unquote(text); run != "" {
			steps = append(steps, TryStep{Run: run, Line: first + i})
		}
	}
	return steps, true, nil
}

// unquote trims s and one pair of backticks wrapping all of it.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`") {
		s = strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}
