package tracker

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// IssueKey identifies one issue in its tracker. It is opaque: GitHub and
// GitLab keys are the issue number as text ("603"), Jira and Linear keys are
// "PROJ-123" / "ENG-42". Compare keys as they come from ParseKey or NumKey,
// never hand-spelled. PR numbers are not keys: the Forge side stays int.
type IssueKey string

// reKey is the grammar of a key-based tracker's identifier: an upper-case
// project prefix, a dash, a number.
var reKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*-[0-9]+$`)

// NumKey is the key of numbered issue n.
func NumKey(n int) IssueKey { return IssueKey(strconv.Itoa(n)) }

// ParseKey reads a key from `603`, `#603`, `proj-123` or `PROJ-123`; ok is
// false for anything else.
func ParseKey(s string) (IssueKey, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return NumKey(n), true
	}
	if u := strings.ToUpper(s); reKey.MatchString(u) {
		return IssueKey(u), true
	}
	return "", false
}

// Number is the issue number of a numbered key; ok is false for a key like
// PROJ-123.
func (k IssueKey) Number() (int, bool) {
	n, err := strconv.Atoi(string(k))
	return n, err == nil && n > 0
}

// String is the bare spelling: `603`, `PROJ-123`.
func (k IssueKey) String() string { return string(k) }

// Ref is the spelling users read: `#603` for a numbered issue, the key itself
// otherwise.
func (k IssueKey) Ref() string {
	if _, ok := k.Number(); ok {
		return "#" + string(k)
	}
	return string(k)
}

// numOf is the issue number a numbered adapter (GitHub, GitLab) sends to its
// CLI. A key like PROJ-123 is not an issue there: KindNotFound.
func numOf(k IssueKey) (int, error) {
	if n, ok := k.Number(); ok {
		return n, nil
	}
	return 0, &Error{Kind: KindNotFound, Code: 1, Message: "not an issue number on this tracker: " + strconv.Quote(string(k))}
}

// Num is the issue number of a numbered issue, 0 for a key like PROJ-123.
// The backlog renders and sorts numbered issues; a key-based tracker's items
// carry their key instead.
func (i Issue) Num() int {
	n, _ := i.Key.Number()
	return n
}

// SortKeys orders keys for display: numbered issues by number, then the rest
// by spelling.
func SortKeys(keys []IssueKey) {
	slices.SortFunc(keys, func(a, b IssueKey) int {
		na, oka := a.Number()
		nb, okb := b.Number()
		switch {
		case oka && okb:
			return na - nb
		case oka:
			return -1
		case okb:
			return 1
		}
		return strings.Compare(string(a), string(b))
	})
}
