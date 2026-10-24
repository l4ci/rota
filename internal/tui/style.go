package tui

import (
	"os"
	"strings"
	"unicode/utf8"
)

// Style draws text with SGR escapes, or leaves it plain when Color is off.
// A screen styles through it and never writes an escape itself, so the plain
// frame a test compares is the styled one with Strip applied.
type Style struct {
	Color bool
	// Links turns Link into terminal hyperlinks (OSC 8). It is independent of
	// Color: NO_COLOR asks for no colour, not for no links.
	Links bool
}

// EnvStyle is the style the environment asks for: no colour under NO_COLOR
// or a dumb or unset TERM, hyperlinks unless the terminal is dumb.
func EnvStyle() Style {
	return Style{Color: !Dumb() && os.Getenv("NO_COLOR") == "", Links: !Dumb()}
}

// Dumb reports a TERM that cannot take escapes: unset or "dumb".
func Dumb() bool {
	t := os.Getenv("TERM")
	return t == "" || t == "dumb"
}

func (s Style) sgr(code, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Bold(t string) string   { return s.sgr("1", t) }
func (s Style) Dim(t string) string    { return s.sgr("2", t) }
func (s Style) Red(t string) string    { return s.sgr("31", t) }
func (s Style) Green(t string) string  { return s.sgr("32", t) }
func (s Style) Yellow(t string) string { return s.sgr("33", t) }
func (s Style) Cyan(t string) string   { return s.sgr("36", t) }

// SafeURL reports whether u may be written into a terminal hyperlink or handed
// to a browser: http or https, no spaces or control characters.
func SafeURL(u string) bool {
	if len(u) > 2048 || !(strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) {
		return false
	}
	for _, r := range u {
		if r <= 0x20 || (r >= 0x7f && r < 0xa0) {
			return false
		}
	}
	return true
}

// Link wraps text in a terminal hyperlink to url. Without Links, or for an
// unsafe url, it is just text. Pad and Width see through the escapes; Fit
// first, then Link.
func (s Style) Link(url, text string) string {
	if !s.Links || text == "" || !SafeURL(url) {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// LinkEnd reports where the hyperlink escape (OSC 8) starting at s[i] ends,
// past its BEL or ESC \ terminator (len(s) when unterminated), or -1 when s[i:]
// does not start one. Other OSC sequences are not touched here.
func LinkEnd(s string, i int) int {
	if s[i] != 0x1b || !strings.HasPrefix(s[i:], "\x1b]8;") {
		return -1
	}
	for j := i + 2; j < len(s); j++ {
		switch {
		case s[j] == 0x07:
			return j + 1
		case s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\':
			return j + 2
		}
	}
	return len(s)
}

// CSIEnd reports where the CSI escape starting at s[i] ends: the index just
// past its final byte (0x40-0x7e), or len(s) when it is unterminated. It
// returns -1 when s[i:] does not start a CSI sequence. Every terminal-safe
// text path in rota scans escapes through it.
func CSIEnd(s string, i int) int {
	if s[i] != 0x1b || i+1 >= len(s) || s[i+1] != '[' {
		return -1
	}
	j := i + 2
	for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
		j++
	}
	if j < len(s) {
		j++
	}
	return j
}

// Strip removes every CSI escape sequence and every hyperlink: the SGR and links
// Style writes and any other CSI (cursor moves, erases) that text from outside
// could carry.
func Strip(s string) string {
	if !strings.Contains(s, "\x1b[") && !strings.Contains(s, "\x1b]8;") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if end := CSIEnd(s, i); end >= 0 {
			i = end
			continue
		}
		if end := LinkEnd(s, i); end >= 0 {
			i = end
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// Sanitize makes text from outside (a forge title, a registry line) safe to
// print: CSI sequences are removed, then the remaining control characters
// (C0, DEL, C1). Tabs become spaces; newlines stay with keepNL and become
// spaces without it.
func Sanitize(s string, keepNL bool) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' && keepNL:
			return r
		case r == '\n' || r == '\t':
			return ' '
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
			return -1
		}
		return r
	}, Strip(s))
}

// Width is the printed width of s in cells: runes, escapes not counted.
func Width(s string) int { return utf8.RuneCountInString(Strip(s)) }

// Fit cuts plain s to w runes, ending in an ellipsis; w <= 0 leaves it alone.
// Fit before styling: it counts escapes as text.
func Fit(s string, w int) string {
	if w <= 0 || utf8.RuneCountInString(s) <= w {
		return s
	}
	r := []rune(s)
	if w == 1 {
		return string(r[:1])
	}
	return string(r[:w-1]) + "…"
}

// Pad fills s with spaces to w cells; a wider s is returned unchanged.
func Pad(s string, w int) string {
	if n := Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
