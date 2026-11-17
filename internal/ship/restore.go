package ship

import (
	"fmt"
	"io"
)

// Reopener is the part of the backlog Restore writes through. The backend
// decides what an already-open item is, so it also words the no-op.
type Reopener interface {
	// Reopen reopens ref; changed is false when it was already open.
	Reopen(ref string) (changed bool, err error)
	// State names where an already-open item is open, for the noop line.
	State() string
}

// Restore reopens each item through the backlog, as hv-uncomplete did; an item
// already open is reported on warn and left alone. The backend must be
// opened after the reset, so it reads the restored config.
func Restore(b Reopener, root string, ids []string, warn io.Writer) error {
	for _, id := range ids {
		changed, err := b.Reopen(id)
		if err != nil {
			return err
		}
		if !changed {
			fmt.Fprintf(warn, "noop: [%s] already %s\n", id, b.State())
		}
	}
	return nil
}
