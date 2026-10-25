package proof

import (
	"path/filepath"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/fsio"
)

// notes keeps the rows in the item's `proof` note.
type notes struct {
	lockDir string
	open    func() (artifact.Notes, error)
}

// NewNotes is the issue-mode store. open resolves the item's notes on first use.
// The note is read, changed and written back as one step under a per-item
// lockfile in lockDir, so overlapping appends keep every row. The note is shared
// by every checkout of the repository, so lockDir must be too: the caller passes
// the rotastate dir under the git common dir, not a per-worktree path.
//
// The tracker calls (open, NoteGet, NotePut) run while the lock is held. A call
// slower than fsio.LockTimeout (10s) makes a waiting append fail with
// fsio.ErrLockTimeout; the append is not retried and no row is lost silently.
func NewNotes(lockDir string, open func() (artifact.Notes, error)) Store {
	return notes{lockDir, open}
}

func (s notes) Update(id string, fn func(string) (string, bool, error)) error {
	lock := filepath.Join(s.lockDir, "proof-"+id+".notes")
	return fsio.Locked(lock, fsio.LockTimeout, func() error { return s.update(id, fn) })
}

func (s notes) update(id string, fn func(string) (string, bool, error)) error {
	n, err := s.open()
	if err != nil {
		return err
	}
	content, _, err := n.NoteGet(id, "proof")
	if err != nil {
		return err
	}
	updated, changed, err := fn(content)
	if err != nil || !changed {
		return err
	}
	_, err = n.NotePut(id, "proof", updated)
	return err
}

func (s notes) Read(id string) (string, bool, error) {
	n, err := s.open()
	if err != nil {
		return "", false, err
	}
	return n.NoteGet(id, "proof")
}
