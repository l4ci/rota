package tui

import (
	"os"
	"strings"
	"unicode/utf8"
)

// Style draws text with SGR escapes, or leaves it plain when Color is off.
// A screen styles through it and never writes an escape itself, so the plain
// frame a test compares is the styled one with Strip applied.
type Style struct{ Color bool }

// EnvStyle is the style the environment asks for: no colour under NO_COLOR
// or a dumb or unset TERM.
func EnvStyle() Style { return Style{Color: !Dumb() && os.Getenv("NO_COLOR") == ""} }

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

// Strip removes the SGR escapes Style writes.
func Strip(s string) string {
	if !strings.Contains(s, "\x1b[") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
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
