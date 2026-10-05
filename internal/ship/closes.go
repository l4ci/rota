package ship

import (
	"errors"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
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
		if errors.Is(err, backlog.ErrNotFound) {
			return "", &UnknownItemError{Ref: ref}
		}
		if err != nil {
			return "", err
		}
		lines = append(lines, "Closes #"+strconv.Itoa(it.Number))
	}
	return strings.Join(lines, "\n"), nil
}
