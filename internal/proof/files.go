package proof

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
)

// files keeps the rows in the item's detail file,
// .rota/<bugs|features|tasks>/<ID>.md under root.
type files struct{ root string }

// Files is the file-mode store.
func Files(root string) Store { return files{root} }

// detailFile is the detail file of a valid file-mode ID.
func detailFile(root, id string) (string, error) {
	if !backlog.ValidID(id, backlog.FileIDDigits) {
		return "", exitcode.Errf(exitcode.ExitUsage, "ID must look like B07, F12 or T03, got %q", id)
	}
	return backlog.DetailPath(root, id), nil
}

func (s files) Update(id string, fn func(string) (string, bool, error)) error {
	path, err := detailFile(s.root, id)
	if err != nil {
		return err
	}
	// File-mode store by definition; it cannot import cli (documented exception to backend_select.go).
	f := &backlog.File{Root: s.root}
	_, title, found := backlog.FindOrigin(f.Corpus(), id)
	if !found {
		return exitcode.Errf(exitcode.ExitResolution, "[%s] not found in BACKLOG.md or ARCHIVE.md", id)
	}
	if title == "" {
		title = id
	}
	return fsio.Locked(path, fsio.LockTimeout, func() error {
		content, rerr := fsio.ReadText(path)
		if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return exitcode.Errf(exitcode.ExitInternal, "cannot read %s: %v", path, rerr)
		}
		if rerr != nil { // missing: start the detail file
			content = fmt.Sprintf("# %s: %s\n\n> Related TODO entry: `[%s]` in `.rota/BACKLOG.md`\n", id, title, id)
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

// Read: a missing detail file is no text, as in the old helper.
func (s files) Read(id string) (string, bool, error) {
	path, err := detailFile(s.root, id)
	if err != nil {
		return "", false, err
	}
	content, rerr := fsio.ReadText(path)
	if errors.Is(rerr, fs.ErrNotExist) {
		return "", false, nil
	}
	if rerr != nil {
		return "", false, exitcode.Errf(exitcode.ExitInternal, "cannot read %s: %v", path, rerr)
	}
	return content, true, nil
}
