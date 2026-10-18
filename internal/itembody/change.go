package itembody

import (
	"regexp"
	"strings"
)

var (
	// ServesRe matches one `Serves:` line, as a bullet (`- Serves: ...`, with
	// optional bold) or bare. Group 1 is the text after the colon.
	ServesRe = regexp.MustCompile(`(?i)^\s*(?:[-*]\s+)?\*{0,2}Serves\*{0,2}:\*{0,2}\s*(.*)$`)
	// ACIDRe matches one acceptance criterion id.
	ACIDRe = regexp.MustCompile(`\bAC-\d+\b`)
	// ChangeHeadRe matches a "Change" heading: the delta an item makes to a
	// milestone capability.
	ChangeHeadRe = regexp.MustCompile(`(?mi)^#{1,6}[ \t]+change[ \t]*$`)
)

// Change is the delta an item declares in its `## Change` section.
type Change struct {
	Kind, Capability string
	// NewName is the new capability of a RENAMED line `RENAMED <old> to <new>`;
	// Capability then holds the old one. Empty without " to ".
	NewName   string
	Serves    []string
	Reason    string
	Migration string
	OK        bool
}

var (
	deltaRe     = regexp.MustCompile(`(?i)^(ADDED|MODIFIED|REMOVED|RENAMED)\s+(.+)$`)
	toRe        = regexp.MustCompile(`(?i)\s+to\s+`)
	reasonRe    = regexp.MustCompile(`(?i)^Reason:(.*)$`)
	migrationRe = regexp.MustCompile(`(?i)^Migration:(.*)$`)
)

// ParseChange reads the `## Change` section of an item body: a first line
// `ADDED|MODIFIED|REMOVED|RENAMED <capability>` (RENAMED reads `<old> to <new>`), then optional `Serves:`,
// `Reason:` and `Migration:` lines. OK is false when the section is absent or
// its first line is not a delta. Pure.
func ParseChange(body string) Change {
	sec, ok := Section(body, ChangeHeadRe)
	if !ok {
		return Change{}
	}
	var c Change
	first := true
	for _, line := range strings.Split(sec, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			line = strings.TrimSpace(line[2:])
		}
		if first {
			first = false
			m := deltaRe.FindStringSubmatch(line)
			if m == nil {
				return Change{}
			}
			c.Kind, c.Capability, c.OK = strings.ToUpper(m[1]), Entry(m[2]), true
			if c.Kind == "RENAMED" {
				if i := toRe.FindStringIndex(m[2]); i != nil {
					c.Capability, c.NewName = Entry(m[2][:i[0]]), Entry(m[2][i[1]:])
				}
			}
			continue
		}
		if m := ServesRe.FindStringSubmatch(line); m != nil {
			c.Serves = append(c.Serves, ACIDRe.FindAllString(m[1], -1)...)
		} else if m := reasonRe.FindStringSubmatch(line); m != nil {
			c.Reason = strings.TrimSpace(m[1])
		} else if m := migrationRe.FindStringSubmatch(line); m != nil {
			c.Migration = strings.TrimSpace(m[1])
		}
	}
	return c
}
