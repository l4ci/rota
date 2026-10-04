package backlog

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/pystr"
)

// Item is one backlog item as every backend presents it.
type Item struct {
	ID     string // file: "B07"; issue: "12"; umbrella issue: "repo:12" (contract rule 11)
	Type   string // "B" | "F" | "T"
	Tag    string // "P1", "Major", "" ...
	Title  string // without the trailing "."; the full title in both backends
	Fields Fields
	Closed bool
	Reason string // closed only: done|handed-off|blocked|dropped ("" when open)
	// ClosedAt is the close date, YYYY-MM-DD ("" when open): the done line's
	// date in file mode, the tracker's closed_at in issue mode.
	ClosedAt string
	Note     string // closure note (file backend)
	Line     string // the bullet: file = origin line as FindOrigin returns it; issue = rendered "- **[F12] ...**" line
	Number   int    // issue mode: the issue number; 0 in file mode
	URL      string // issue mode
}

// Key is how an item is spelled inside bullets and Related cells: the type
// letter plus the number in issue mode ("F12" for issue 12), the ID itself in
// file mode ("B07").
func (it Item) Key() string {
	if it.Number > 0 {
		return it.Type + strconv.Itoa(it.Number)
	}
	return it.ID
}

// Ref is a parsed item reference.
type Ref struct {
	Repo   string // umbrella sub-repo, "" when the reference is unqualified
	Letter string // upper-case type letter, "" when not given
	Number int
}

// ErrNotFound is wrapped by the errors that report an unknown item or a
// missing BACKLOG.md; test with errors.Is.
var ErrNotFound = errors.New("item not found")

// ErrWrongBackend is for callers that need one backend and got the other.
var ErrWrongBackend = errors.New("wrong backlog backend")

var (
	qualHashRe  = regexp.MustCompile(`\A([^` + pystr.SpaceClass + `#:]+)#(\p{Nd}+)\z`)
	qualColonRe = regexp.MustCompile(`\A([^` + pystr.SpaceClass + `#:]+):([^` + pystr.SpaceClass + `]+)\z`)
	itemRefRe   = regexp.MustCompile(`(?i)\A(?:#(\p{Nd}+)|([` + ItemLetters + `])?(\p{Nd}+))\z`)
)

// ParseRef parses an item reference: B7, b7, #7 or 7, optionally qualified
// with a sub-repo as repo:B7, repo:#7, repo:7 or repo#7. A repo name is one
// or more characters other than whitespace, "#" and ":". Anything else is an
// error. Surrounding whitespace is ignored.
func ParseRef(s string) (Ref, error) {
	s = pystr.Strip(s)
	if m := qualHashRe.FindStringSubmatch(s); m != nil {
		n, err := atoi(m[2])
		if err != nil {
			return Ref{}, err
		}
		return Ref{Repo: m[1], Number: n}, nil
	}
	if m := qualColonRe.FindStringSubmatch(s); m != nil {
		n, letter, err := resolveItemRef(m[2])
		if err != nil {
			return Ref{}, err
		}
		return Ref{Repo: m[1], Letter: letter, Number: n}, nil
	}
	n, letter, err := resolveItemRef(s)
	if err != nil {
		return Ref{}, err
	}
	return Ref{Letter: letter, Number: n}, nil
}

// resolveItemRef is hvlib_backend.resolve_item_ref: "#42", "42" or "F42" give
// (42, "") or (42, "F"); the letter is upper-cased.
func resolveItemRef(ref string) (n int, letter string, err error) {
	s := pystr.Strip(ref)
	m := itemRefRe.FindStringSubmatch(s)
	if m == nil {
		return 0, "", fmt.Errorf("not an item reference: %q", s)
	}
	digits := m[1]
	if digits == "" {
		digits = m[3]
	}
	if n, err = atoi(digits); err != nil {
		return 0, "", err
	}
	if m[2] != "" {
		letter = string(upper(m[2][0]))
	}
	return n, letter, nil
}

func upper(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 'a' + 'A'
	}
	return b
}

// atoi reads a run of Unicode decimal digits (Python's int() accepts them).
func atoi(digits string) (int, error) {
	n := 0
	for _, r := range digits {
		d := pystr.DigitValue(r)
		if n > (int(^uint(0)>>1)-d)/10 {
			return 0, fmt.Errorf("number out of range: %s", digits)
		}
		n = n*10 + d
	}
	return n, nil
}

func decodeRune(s string) (rune, int) { return utf8.DecodeRuneInString(s) }
