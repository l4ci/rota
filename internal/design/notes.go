package design

import (
	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
)

// notes keeps each design as the `design` note of the item's issue.
type notes struct {
	open func() (artifact.Notes, error)
}

// NewNotes is the issue-mode store. open resolves the item's notes on first
// use, so an ID the module rejects never reaches the tracker.
func NewNotes(open func() (artifact.Notes, error)) Store { return notes{open} }

// CheckID: the item references the issue backend resolves, a number, a lettered
// number or a tracker key.
func (notes) CheckID(id string) error {
	if _, err := backlog.ParseIssueRef(id); err != nil {
		return exitcode.Errf(exitcode.ExitUsage, "ID must be an issue number or tracker key: N, #N, [%s]N or PROJ-123 (e.g. 233, #233, B7); designs are per-item, not per-slice or per-milestone, got %q", backlog.ItemLetters, id)
	}
	return nil
}

// docs resolves the item's notes on first use and keeps the design in its
// `design` note.
func (s notes) docs() artifact.NoteDocs {
	return artifact.NoteDocs{Kind: "design", Open: func(id string) (artifact.NoteSlot, string, error) {
		id = normID(id)
		n, err := s.open()
		if err != nil {
			return nil, "", err
		}
		return artifact.ItemNote{Notes: n, Ref: id, Kind: "design"}, id, nil
	}}
}

func (s notes) Create(id, text string) error {
	id = normID(id)
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
