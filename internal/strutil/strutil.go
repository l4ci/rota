// Package strutil holds the small text helpers several packages used to carry
// their own copies of: the first meaningful line of command output, a
// rune-safe clip and a short commit SHA.
package strutil

import "github.com/l4ci/rota/internal/pystr"

// shaLen is how many characters of a commit SHA the messages show.
const shaLen = 7

// FirstLine returns the first line of s that has text, stripped of Python
// whitespace, or "" when there is none. It trims a failed command's stderr to
// the line worth quoting.
func FirstLine(s string) string {
	for _, l := range pystr.Splitlines(s) {
		if l = pystr.Strip(l); l != "" {
			return l
		}
	}
	return ""
}

// Clip is Python's s[:n] on characters: it cuts s to n runes and never splits
// one.
func Clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// ShortSHA abbreviates a commit or tree SHA to 7 characters; a shorter value
// comes back as is.
func ShortSHA(sha string) string {
	if len(sha) > shaLen {
		return sha[:shaLen]
	}
	return sha
}
