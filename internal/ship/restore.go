package ship

import (
	"fmt"
	"io"
	"regexp"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/pystr"
	"github.com/l4ci/rota/internal/rotatree"
	"github.com/l4ci/rota/internal/section"
)

// CycleIDs lists the item IDs whose done line in BACKLOG.md or ARCHIVE.md
// carries one of the cycle's short hashes, first seen first.
func CycleIDs(root string, hashes map[string]bool) []string {
	var ids []string
	seen := map[string]bool{}
	for _, name := range []string{"BACKLOG.md", "ARCHIVE.md"} {
		text, err := fsio.ReadText(rotatree.File(root, name))
		if err != nil {
			continue
		}
		for _, l := range pystr.Splitlines(text) {
			if d, ok := backlog.ParseDone(l); ok && d.ID != "" && hashes[d.Hash] && !seen[d.ID] {
				seen[d.ID] = true
				ids = append(ids, d.ID)
			}
		}
	}
	return ids
}

// Reopener is the part of the backlog Restore writes through.
type Reopener interface {
	Name() string
	Reopen(ref string) (changed bool, err error)
}

// Restore reopens each item through the backlog, as hv-uncomplete did; an item
// already active is reported on warn and left alone. The backend must be
// opened after the reset, so it reads the restored config.
func Restore(b Reopener, root string, ids []string, warn io.Writer) error {
	for _, id := range ids {
		changed := false
		if b.Name() != "file" || !active(root, id) {
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

// active reports whether BACKLOG.md under root holds id as an active bullet
// outside ## Completed, the no-op case of hv-uncomplete. Checking it unlocked
// keeps the no-op from leaving a .lock sidecar, which the old helper never
// created and which dirties a tree that does not ignore it.
func active(root, id string) bool {
	content, err := fsio.ReadText(rotatree.Backlog(root))
	if err != nil {
		return false
	}
	cs, ce, hasC := section.Find(content, "Completed")
	for _, m := range regexp.MustCompile(`(?m)^- \*\*\[`+regexp.QuoteMeta(id)+`\]`).FindAllStringIndex(content, -1) {
		if !hasC || m[0] < cs || m[0] >= ce {
			return true
		}
	}
	return false
}
