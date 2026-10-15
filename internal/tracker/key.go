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

// NormRef is the one spelling issue and item ids are compared and keyed by:
// trimmed, no leading `#`, upper case. worker.NormID is this rule.
func NormRef(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimSpace(strings.TrimPrefix(id, "#"))
	return strings.ToUpper(id)
}

// ParseKey reads a key from `603`, `#603`, `proj-123` or `PROJ-123`; ok is
// false for anything else.
func ParseKey(s string) (IssueKey, bool) {
	s = NormRef(s)
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return NumKey(n), true
	}
	if reKey.MatchString(s) {
		return IssueKey(s), true
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

// RefOf is Ref for an issue id as the registry and reports hold it: `#603`
// for `603` or `#603`, `PROJ-123` for a key. Text that is no key (an item id
// like B07) keeps its `#` prefix as before.
func RefOf(id string) string {
	if k, ok := ParseKey(id); ok {
		return k.Ref()
	}
	return "#" + strings.TrimPrefix(strings.TrimSpace(id), "#")
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

// CompareKeys orders keys for display: numbered issues by number, then the
// rest by project and, within one project, by number (ENG-9 before ENG-10).
func CompareKeys(a, b IssueKey) int {
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
	if pa, pb := a.Project(), b.Project(); pa != "" && pa == pb {
		sa, _ := a.Seq()
		sb, _ := b.Seq()
		if sa != sb {
			return sa - sb
		}
	}
	return strings.Compare(string(a), string(b))
}

// SortKeys sorts keys by CompareKeys.
func SortKeys(keys []IssueKey) { slices.SortFunc(keys, CompareKeys) }

// Project is the project prefix of a key-based tracker's key (PROJ of
// PROJ-123); "" for a numbered key or anything that is not a key.
func (k IssueKey) Project() string {
	if !reKey.MatchString(string(k)) {
		return ""
	}
	p, _, _ := strings.Cut(string(k), "-")
	return p
}

// Seq is the number of a key-based tracker's key (123 of PROJ-123); ok is
// false for a numbered key or anything that is not a key.
func (k IssueKey) Seq() (int, bool) {
	if !reKey.MatchString(string(k)) {
		return 0, false
	}
	_, n, _ := strings.Cut(string(k), "-")
	v, err := strconv.Atoi(n)
	return v, err == nil
}
