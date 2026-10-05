package proof

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
)

var idRe = regexp.MustCompile(`^[BFT]\d+$`)

// files keeps the rows in the item's detail file,
// .rota/<bugs|features|tasks>/<ID>.md under root.
type files struct{ root string }

// Files is the file-mode store.
func Files(root string) Store { return files{root} }

// kindOf is the detail directory for a valid file-mode ID.
func kindOf(id string) (kind string, err error) {
	if !idRe.MatchString(id) {
		return "", exitcode.Errf(exitcode.ExitUsage, "ID must look like B07, F12 or T03, got %q", id)
	}
	t, _ := backlog.TypeByLetter(id[:1])
	return t.Kind, nil
}

func detailPath(root, kind, id string) string { return filepath.Join(root, ".rota", kind, id+".md") }

func (s files) Update(id string, fn func(string) (string, bool, error)) error {
	kind, err := kindOf(id)
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
	path := detailPath(s.root, kind, id)
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
	kind, err := kindOf(id)
	if err != nil {
		return "", false, err
	}
	path := detailPath(s.root, kind, id)
	content, rerr := fsio.ReadText(path)
	if errors.Is(rerr, fs.ErrNotExist) {
		return "", false, nil
	}
	if rerr != nil {
		return "", false, exitcode.Errf(exitcode.ExitInternal, "cannot read %s: %v", path, rerr)
	}
	return content, true, nil
}
