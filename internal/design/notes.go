package design

import (
	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/exitcode"
)

// notes keeps each design as the `design` note of the item's issue.
type notes struct {
	open func() (artifact.Notes, error)
}

// NewNotes is the issue-mode store. open resolves the item's notes on first
// use, so an ID the module rejects never reaches the tracker.
func NewNotes(open func() (artifact.Notes, error)) Store { return notes{open} }

func (notes) Digits() int { return 1 }

func noteMissing(id string) *exitcode.Error {
	return exitcode.Errf(exitcode.ExitResolution, "design note for %s not found", id)
}

func (s notes) Create(id, text string) error {
	n, err := s.open()
	if err != nil {
		return err
	}
	if _, ok, err := n.NoteGet(id, "design"); err != nil {
		return err
	} else if ok {
		return exitcode.Errf(exitcode.ExitRefused, "design note for %s already exists", id)
	}
	_, err = n.NotePut(id, "design", text)
	return err
}

// Read adds the newline the old helper printed after a note.
func (s notes) Read(id string) (string, error) {
	n, err := s.open()
	if err != nil {
		return "", err
	}
	text, ok, err := n.NoteGet(id, "design")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", noteMissing(id)
	}
	return text + "\n", nil
}

func (s notes) Replace(id, text string) (bool, error) {
	n, err := s.open()
	if err != nil {
		return false, err
	}
	if _, ok, err := n.NoteGet(id, "design"); err != nil {
		return false, err
	} else if !ok {
		return false, noteMissing(id)
	}
	return n.NotePut(id, "design", text)
}

func (s notes) Remove(id string) error {
	n, err := s.open()
	if err != nil {
		return err
	}
	removed, err := n.NoteRm(id, "design")
	if err != nil {
		return err
	}
	if !removed {
		return noteMissing(id)
	}
	return nil
}
