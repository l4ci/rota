package design

import (
	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/rotatree"
)

// ValidID reports whether id is a file-mode design ID.
func ValidID(id string) bool { return backlog.ValidID(id, backlog.FileIDDigits) }

// files keeps each design in .rota/designs/<ID>.md under root.
type files struct{ root string }

// Files is the file-mode store.
func Files(root string) Store { return files{root} }

// CheckID: a file design is [BFT] and at least two digits, never per-slice or
// per-milestone.
func (files) CheckID(id string) error {
	if backlog.ValidID(id, backlog.FileIDDigits) {
		return nil
	}
	return exitcode.Errf(exitcode.ExitUsage, "ID must match [%s]\\d{%d,} (e.g. B07, F03, T11); designs are per-item, not per-slice or per-milestone, got %q", backlog.ItemLetters, backlog.FileIDDigits, id)
}

func docs(root string) artifact.FileDocs {
	return artifact.FileDocs{Root: root, Dir: rotatree.DesignsDir, Kind: "design"}
}

func path(root, id string) string { return docs(root).Path(id) }

func (s files) Create(id, text string) error {
	_, err := docs(s.root).Create(id, func() (string, string) { return id, text })
	return err
}

func (s files) Read(id string) (string, error) { return docs(s.root).Read(id) }

func (s files) Replace(id, text string) (bool, error) { return docs(s.root).Replace(id, text) }

func (s files) Remove(id string) error { return docs(s.root).Remove(id) }

func (s files) Update(id string, fn func(string) (string, error)) (bool, error) {
	return docs(s.root).Update(id, fn)
}

// Entry is one row of List.
type Entry struct{ ID, Title, Status, Created string }

// List reads .rota/designs/*.md in name order; files without frontmatter are
// skipped. status defaults to draft and id to the file stem.
func List(root string) ([]Entry, error) {
	docs, err := artifact.ListDocs(rotatree.File(root, rotatree.DesignsDir))
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, d := range docs {
		e := Entry{ID: frontmatter.Str(d.FM, "id"), Title: frontmatter.Str(d.FM, "title"),
			Status: frontmatter.Str(d.FM, "status"), Created: frontmatter.Str(d.FM, "created")}
		if e.ID == "" {
			e.ID = d.Stem
		}
		if e.Status == "" {
			e.Status = "draft"
		}
		out = append(out, e)
	}
	return out, nil
}
