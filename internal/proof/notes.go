package proof

import (
	"path/filepath"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/fsio"
)

// notes keeps the rows in the item's `proof` note.
type notes struct {
	root string
	open func() (artifact.Notes, error)
}

// NewNotes is the issue-mode store. open resolves the item's notes on first use.
// The note is read, changed and written back as one step under a per-item
// lockfile beside the project's .rota, so overlapping appends keep every row.
func NewNotes(root string, open func() (artifact.Notes, error)) Store { return notes{root, open} }

func (s notes) Update(id string, fn func(string) (string, bool, error)) error {
	lock := filepath.Join(s.root, ".rota", "proof-"+id+".notes")
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
