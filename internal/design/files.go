package design

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/artifact"
	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/frontmatter"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/section"
)

// ValidID reports whether id is a file-mode design ID.
func ValidID(id string) bool { return backlog.ValidID(id, backlog.FileIDDigits) }

// files keeps each design in .rota/designs/<ID>.md under root.
type files struct{ root string }

// Files is the file-mode store.
func Files(root string) Store { return files{root} }

func (files) Digits() int { return backlog.FileIDDigits }

func path(root, id string) string { return filepath.Join(root, ".rota", "designs", id+".md") }

func notFound(id string) *exitcode.Error {
	return exitcode.Errf(exitcode.ExitResolution, "design %s not found (.rota/designs/%s.md)", id, id)
}

func (s files) Create(id, text string) error {
	p := path(s.root, id)
	return fsio.Locked(p, fsio.LockTimeout, func() error {
		if _, err := os.Stat(p); err == nil {
			return exitcode.Errf(exitcode.ExitRefused, ".rota/designs/%s.md already exists", id)
		}
		return fsio.WriteFileAtomic(p, []byte(text))
	})
}

// Read is the stored design, verbatim.
func (s files) Read(id string) (string, error) {
	b, err := os.ReadFile(path(s.root, id))
	if err != nil {
		return "", notFound(id)
	}
	return string(b), nil
}

func (s files) Replace(id, text string) (changed bool, err error) {
	p := path(s.root, id)
	if _, serr := os.Stat(p); serr != nil { // no lock file or directory for a missing design
		return false, notFound(id)
	}
	err = fsio.Locked(p, fsio.LockTimeout, func() error {
		old, rerr := os.ReadFile(p)
		if rerr != nil {
			return notFound(id)
		}
		if string(old) == text {
			return nil
		}
		changed = true
		return fsio.WriteFileAtomic(p, []byte(text))
	})
	return
}

func (s files) Remove(id string) error {
	p := path(s.root, id)
	if _, err := os.Stat(p); err != nil {
		return notFound(id)
	}
	return fsio.Locked(p, fsio.LockTimeout, func() error {
		if err := os.Remove(p); err != nil {
			return notFound(id)
		}
		return nil
	})
}

// Entry is one row of List.
type Entry struct{ ID, Title, Status, Created string }

// List reads .rota/designs/*.md in name order; files without frontmatter are
// skipped. status defaults to draft and id to the file stem.
func List(root string) ([]Entry, error) {
	docs, err := artifact.ListDocs(filepath.Join(root, ".rota", "designs"))
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

// Amend replaces or appends to the body of "## <heading>" in a design, as
// hv-design-amend did: trailing newlines of text are dropped and the block
// is "\n<text>\n\n". mode is "append" or "replace". A missing design or
// heading is exit 3. changed is false when the file would not change. Amend
// is file-only: issue-mode designs are edited whole with Put.
func Amend(root, id, heading, mode, text string) (changed bool, err error) {
	if err = CheckID(files{root}, id); err != nil {
		return
	}
	if mode != "append" && mode != "replace" {
		return false, exitcode.Errf(exitcode.ExitUsage, "mode must be 'append' or 'replace', got %q", mode)
	}
	p := path(root, id)
	if _, serr := os.Stat(p); serr != nil {
		return false, notFound(id)
	}
	block := "\n" + strings.TrimRight(text, "\n") + "\n\n"
	err = fsio.Locked(p, fsio.LockTimeout, func() error {
		content, rerr := fsio.ReadText(p)
		if rerr != nil {
			return notFound(id)
		}
		_, _, ok := section.Find(content, heading)
		if !ok {
			return exitcode.Errf(exitcode.ExitResolution, "section '## %s' not found in .rota/designs/%s.md", heading, id)
		}
		var updated string
		if mode == "replace" {
			updated = section.Replace(content, heading, block)
		} else {
			updated, _ = artifact.AppendSection(content, heading, block)
		}
		if updated == content {
			return nil
		}
		changed = true
		return fsio.WriteFileAtomic(p, []byte(updated))
	})
	return
}
