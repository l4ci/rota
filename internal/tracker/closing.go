package tracker

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The closing-keyword patterns of hvlib_tracker. Go's regexp has no
// lookbehind, so "not preceded by a word character" is checked by hand, and
// \s is spelled out as Python's Unicode whitespace.
const (
	pySpace    = `[\t\n\v\f\r\x{1c}-\x{1f}\x{85}\p{Z}]`
	closeVerbs = `close[sd]?|fix(?:es|ed)?|resolve[sd]?`
)

var (
	reClosingGH = regexp.MustCompile(`(?i)(?:` + closeVerbs + `)\b:?` + pySpace + `+#([0-9]+)`)
	reClosingGL = regexp.MustCompile(`(?i)(?:` + closeVerbs + `|implement(?:s|ed)?)\b:?` + pySpace + `+#([0-9]+)`)
)

func closingGH(body string) []int { return closedNumbers(reClosingGH, body) }
func closingGL(body string) []int { return closedNumbers(reClosingGL, body) }

func closedNumbers(re *regexp.Regexp, body string) []int {
	var out []int
	seen := map[int]bool{}
	for _, m := range re.FindAllStringSubmatchIndex(body, -1) {
		if r, _ := utf8.DecodeLastRuneInString(body[:m[0]]); m[0] > 0 && isWord(r) {
			continue
		}
		n, err := strconv.Atoi(body[m[2]:m[3]])
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// isWord is Python's Unicode \w.
func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// startsWithWord reports whether s begins with a word character.
func startsWithWord(s string) bool {
	r, n := utf8.DecodeRuneInString(s)
	return n > 0 && isWord(r)
}

// isPySpace is Python's str.isspace for one rune.
func isPySpace(r rune) bool {
	return (r >= '\t' && r <= '\r') || (r >= 0x1c && r <= 0x1f) || r == 0x85 || unicode.In(r, unicode.Z)
}

// ClosesByKey is the closing convention of a key-based tracker (Jira, Linear):
// a PR closes the issue its key names in the head branch or the title,
// compared without case and not inside a longer key (PROJ-12 is not PROJ-123).
func ClosesByKey(key IssueKey, c ClosingSource) bool {
	if key == "" {
		return false
	}
	return hasKey(c.Branch, key) || hasKey(c.Title, key)
}

// reKeyToken finds key-shaped tokens (PROJ-12) not inside a longer word; `-`
// and `_` may precede one (fix-PROJ-12), so the leading boundary is any
// character but a letter or digit.
var reKeyToken = regexp.MustCompile(`(?:^|[^A-Za-z0-9])([A-Za-z][A-Za-z0-9_]*-[0-9]+)`)

func hasKey(text string, key IssueKey) bool {
	for _, m := range reKeyToken.FindAllStringSubmatch(text, -1) {
		if strings.EqualFold(m[1], string(key)) {
			return true
		}
	}
	return false
}
