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

// docs resolves the item's notes on first use and keeps the design in its
// `design` note.
func (s notes) docs() artifact.NoteDocs {
	return artifact.NoteDocs{Kind: "design", Open: func(id string) (artifact.NoteSlot, string, error) {
		n, err := s.open()
		if err != nil {
			return nil, "", err
		}
		return artifact.ItemNote{Notes: n, Ref: id, Kind: "design"}, id, nil
	}}
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

func (s notes) Read(id string) (string, error) { return s.docs().Read(id) }

func (s notes) Replace(id, text string) (bool, error) { return s.docs().Replace(id, text) }

func (s notes) Remove(id string) error { return s.docs().Remove(id) }
