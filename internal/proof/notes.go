package proof

import "github.com/l4ci/rota/internal/artifact"

// notes keeps the rows in the item's `proof` note.
type notes struct {
	open func() (artifact.Notes, error)
}

// NewNotes is the issue-mode store. open resolves the item's notes on first use.
func NewNotes(open func() (artifact.Notes, error)) Store { return notes{open} }

func (s notes) Update(id string, fn func(string) (string, bool, error)) error {
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
