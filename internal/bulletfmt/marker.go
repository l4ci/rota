package bulletfmt

import "strings"

// Marker sets for CutMarker. Each caller names the set it accepts, so the
// rule "which characters open a list item" lives here and no caller spells it.
const (
	DashOnly  = "-"   // the knowledge bullet marker
	DashStar  = "-*"  // issue-body sections
	AnyMarker = "-*+" // decision text blocks
)

// CutMarker reports whether line starts with a list marker from markers
// followed by a space, and returns the text after the marker. The line is not
// trimmed: callers that accept indented items trim first.
func CutMarker(line, markers string) (rest string, ok bool) {
	if len(line) < 2 || line[1] != ' ' || strings.IndexByte(markers, line[0]) < 0 {
		return "", false
	}
	return line[2:], true
}

// Count is the number of bullets in a topic's text: the column-0 "- " lines.
// Indented lines continue a bullet and never count. Both knowledge stores
// report per-topic bullet counts through it.
func Count(text string) int {
	if text == "" {
		return 0
	}
	return len(Spans(strings.Split(text, "\n")))
}
