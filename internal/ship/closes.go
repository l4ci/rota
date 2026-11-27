package ship

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitmap"
)

// UnknownItemError is an --items ID the issue tracker does not have.
type UnknownItemError struct{ Ref string }

func (e *UnknownItemError) Error() string {
	return "--items " + e.Ref + ": no such item in the issue tracker"
}

// ClosesLines is one `Closes #<n>` line per item. sub, set in an umbrella,
// is the sub-repo the PR opens in: an item qualified with another one does
// not count. A tracker failure passes through unchanged.
func ClosesLines(b backlog.Backend, sub string, ids []string) (string, error) {
	var lines []string
	for _, ref := range ids {
		it, err := b.Get(ref)
		if err == nil && sub != "" && !strings.HasPrefix(it.ID, sub+":") {
			err = backlog.ErrNotFound // qualified with another sub-repo
		}
		if exitmap.IsNotFound(err) {
			return "", &UnknownItemError{Ref: ref}
		}
		if err != nil {
			return "", err
		}
		lines = append(lines, "Closes #"+strconv.Itoa(it.Number))
	}
	return strings.Join(lines, "\n"), nil
}

var (
	// refsLineRe is a body line that opens with `Refs #n` (a list marker, a
	// colon and a comma/"and" list of numbers allowed).
	refsLineRe = regexp.MustCompile(`(?im)^[ \t]*(?:[-*+][ \t]+)?refs\b:?[ \t]*#\d+(?:[ \t]*(?:,|,?[ \t]+and\b)[ \t]*#\d+)*`)
	refNumRe   = regexp.MustCompile(`#\d+`)
)

// dropReferenced removes from lines (ClosesLines output) each issue body
// names on a Refs line: the author says this PR must not close it.
func dropReferenced(lines, body string) string {
	refs := map[string]bool{}
	for _, l := range refsLineRe.FindAllString(body, -1) {
		for _, n := range refNumRe.FindAllString(l, -1) {
			refs[n] = true
		}
	}
	var kept []string
	for _, l := range strings.Split(lines, "\n") {
		if !refs[strings.TrimPrefix(l, "Closes ")] {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}
