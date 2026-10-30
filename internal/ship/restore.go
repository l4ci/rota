package ship

import (
	"fmt"
	"io"

	"github.com/l4ci/rota/internal/backlog"
)

// Reopener is the part of the backlog Restore writes through.
type Reopener interface {
	Capabilities() backlog.Capabilities
	// Active reports whether id is already an open item.
	Active(ref string) bool
	Reopen(ref string) (changed bool, err error)
}

// Restore reopens each item through the backlog, as hv-uncomplete did; an item
// already active is reported on warn and left alone. The backend must be
// opened after the reset, so it reads the restored config.
func Restore(b Reopener, root string, ids []string, warn io.Writer) error {
	for _, id := range ids {
		changed := false
		if b.Capabilities().IssueIDs || !b.Active(id) {
			var err error
			if changed, err = b.Reopen(id); err != nil {
				return err
			}
		}
		if !changed {
			fmt.Fprintf(warn, "noop: [%s] already active in BACKLOG.md\n", id)
		}
	}
	return nil
}
