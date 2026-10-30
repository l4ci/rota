// Package pystr holds the Python string semantics the ported helpers lean on:
// Unicode whitespace and word classes, str.strip, str.splitlines and the
// universal-newline translation of Path.read_text. Go's regexp \s, \d and \w
// are ASCII, so ports that must match Python byte for byte use these instead.
package pystr

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// SpaceClass is the body of a regexp character class that matches exactly
// what Python's str \s (and str.isspace) matches. Use it as "[" + SpaceClass + "]".
const SpaceClass = `\t-\r\x1c-\x1f \x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`

// IsSpace reports whether r is whitespace to Python: unicode.IsSpace plus
// the C0 separators U+001C to U+001F.
func IsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// IsWord reports whether r is a Python \w character: a letter, any numeric
// character, or an underscore. Combining marks are not word characters.
func IsWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_'
}

// IsDigit reports whether r matches Python's \d (Unicode category Nd).
func IsDigit(r rune) bool { return unicode.Is(unicode.Nd, r) }

// DigitValue is the decimal value of an Nd rune. Nd characters come in
// aligned runs of ten, so the value is the offset from the run's start mod ten.
func DigitValue(r rune) int {
	start := r
	for IsDigit(start - 1) {
		start--
	}
	return int(r-start) % 10
}

// Strip is str.strip().
func Strip(s string) string { return strings.TrimFunc(s, IsSpace) }

// Rstrip is str.rstrip().
func Rstrip(s string) string { return strings.TrimRightFunc(s, IsSpace) }

// UniversalNewlines is the universal-newline translation of Path.read_text and
// subprocess.run(text=True): "\r\n" and a lone "\r" both become "\n".
func UniversalNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// Splitlines is str.splitlines(): it splits at \n, \r, \r\n, \v, \f,
// U+001C to U+001E, U+0085, U+2028 and U+2029 and drops the terminators.
func Splitlines(s string) []string {
	var out []string
	start, i := 0, 0
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, s[start:i])
			i += n
			start = i
		case '\r':
			out = append(out, s[start:i])
			i++
			if i < len(s) && s[i] == '\n' {
				i++
			}
			start = i
		default:
			i += n
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// SplitCSV splits a comma-separated flag value, strips each part and drops
// the blank ones. The result is never nil, so it encodes as [] in JSON.
func SplitCSV(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = Strip(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseDate is parse_date: an ISO date, YYYY-MM-DD or YYYYMMDD, after
// stripping; ok is false for anything else. (Python 3.11 also reads ISO week
// dates; no rota file carries one.)
func ParseDate(s string) (time.Time, bool) {
	s = Strip(s)
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if len(s) == len(layout) {
			if t, err := time.Parse(layout, s); err == nil && t.Year() >= 1 {
				return t, true
			}
		}
	}
	return time.Time{}, false
}
