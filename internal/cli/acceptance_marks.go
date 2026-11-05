package cli

import (
	"path/filepath"

	"github.com/l4ci/rota/internal/acceptance"
	"github.com/l4ci/rota/internal/fsio"
)

// markAcceptance owns the fresh read/merge/write of one acceptance mark. id is
// canonical, so aliases of an item lock the same file, including across linked
// worktrees. Keep tracker I/O here, outside the pure acceptance grammar.
func markAcceptance(c *Ctx, root, id string, mark acceptance.Mark) (changed bool, err error) {
	lock := filepath.Join(itemNoteLockDir(c, root), "acceptance-"+id+".notes")
	err = fsio.Locked(lock, fsio.LockTimeout, func() error {
		// Proof validation may have cached the old notes, and another mark
		// may have finished while we waited for the lock. Refresh only now.
		c.deps().freshReads()
		n, err := itemNotes(c, id, &who{})()
		if err != nil {
			return err
		}
		note, _, err := n.NoteGet(id, "acceptance")
		if err != nil {
			return err
		}
		marks := acceptance.Upsert(acceptance.ParseMarks(note), mark)
		changed, err = n.NotePut(id, "acceptance", acceptance.RenderMarks(marks))
		return err
	})
	return changed, err
}
