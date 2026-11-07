package proof

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
)

// Sidecar is what the file-mode store asks of the caller, which owns the item
// model: where an item's proof sidecar lives and whether the item exists. The
// store itself judges nothing about ids.
type Sidecar struct {
	// Path is the sidecar of id, or an error when id cannot name an item.
	Path func(id string) (string, error)
	// Title is the item's title (empty when it has none), or an error when no
	// such item exists. Update asks it before writing.
	Title func(id string) (string, error)
	// Text is the item's current proof text: the sidecar, else the detail file
	// an older rota wrote rows into. ok is false when neither exists.
	Text func(id string) (content string, ok bool, err error)
}

// files keeps the rows in a gitignored sidecar beside the item's detail file,
// so recording proof never dirties the worktree. Rows recorded in the detail
// file before the sidecar existed are still read, and seed the sidecar on its
// first write.
type files struct{ sc Sidecar }

// Files is the file-mode store.
func Files(sc Sidecar) Store { return files{sc} }

func (s files) Update(id string, fn func(string) (string, bool, error)) error {
	path, err := s.sc.Path(id)
	if err != nil {
		return err
	}
	title, err := s.sc.Title(id)
	if err != nil {
		return err
	}
	if title == "" {
		title = id
	}
	return fsio.Locked(path, fsio.LockTimeout, func() error {
		content, rerr := fsio.ReadText(path)
		if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return exitcode.Errf(exitcode.ExitInternal, "cannot read %s: %v", path, rerr)
		}
		if rerr != nil { // missing: start the sidecar, from the legacy rows when the detail file has any
			content = fmt.Sprintf("# %s: %s\n", id, title)
			legacy, _, lerr := s.sc.Text(id)
			if lerr != nil {
				return exitcode.Errf(exitcode.ExitInternal, "cannot read proof of %s: %v", id, lerr)
			}
			if rows := rowLines(legacy); len(rows) > 0 {
				content += "\n## Proof\n\n" + strings.Join(rows, "\n") + "\n"
			}
		}
		updated, changed, err := fn(content)
		if err != nil || !changed {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
			return err
		}
		return fsio.WriteFileAtomic(path, []byte(updated))
	})
}

// Read: no sidecar and no detail file is no text, as in the old helper.
func (s files) Read(id string) (string, bool, error) {
	path, err := s.sc.Path(id)
	if err != nil {
		return "", false, err
	}
	content, ok, rerr := s.sc.Text(id)
	if rerr != nil {
		return "", false, exitcode.Errf(exitcode.ExitInternal, "cannot read %s: %v", path, rerr)
	}
	return content, ok, nil
}
