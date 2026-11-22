package backlog

import (
	"errors"
	"github.com/l4ci/rota/internal/itemid"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/tracker"
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
	Note     string   // closure note (file backend)
	Line     string   // the bullet: file = origin line as FindOrigin returns it; issue = rendered "- **[F12] ...**" line
	Number   int      // issue mode: the issue number; 0 in file mode
	URL      string   // issue mode
	Labels   []string // issue mode: the issue's labels
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

// IssueKey is the tracker key behind an issue-mode item: its number when
// numbered (also under an umbrella, whose ID is qualified), else its ID. "" for
// a file-mode item.
func (it Item) IssueKey() tracker.IssueKey {
	if it.Number > 0 {
		return tracker.NumKey(it.Number)
	}
	if k, ok := tracker.ParseKey(it.ID); ok {
		return k
	}
	return ""
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
)

// ParseRef parses an item reference: B7, b7, #7 or 7, optionally qualified
// with a sub-repo as repo:B7, repo:#7, repo:7 or repo#7. A repo name is one
// or more characters other than whitespace, "#" and ":". Anything else is an
// error. Surrounding whitespace is ignored.
func ParseRef(s string) (Ref, error) {
	s = pystr.Strip(s)
	if m := qualHashRe.FindStringSubmatch(s); m != nil {
		n, err := itemid.Atoi(m[2])
		if err != nil {
			return Ref{}, err
		}
		return Ref{Repo: m[1], Number: n}, nil
	}
	if m := qualColonRe.FindStringSubmatch(s); m != nil {
		n, letter, err := itemid.ResolveItemRef(m[2])
		if err != nil {
			return Ref{}, err
		}
		return Ref{Repo: m[1], Letter: letter, Number: n}, nil
	}
	n, letter, err := itemid.ResolveItemRef(s)
	if err != nil {
		return Ref{}, err
	}
	return Ref{Letter: letter, Number: n}, nil
}

func decodeRune(s string) (rune, int) { return utf8.DecodeRuneInString(s) }
