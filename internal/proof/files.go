package proof

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
)

// files keeps the rows in a gitignored sidecar beside the item's detail file,
// .rota/<bugs|features|tasks>/<ID>.proof under root, so recording proof never
// dirties the worktree. Rows recorded in the detail file before the sidecar
// existed are still read, and seed the sidecar on its first write.
type files struct{ root string }

// Files is the file-mode store.
func Files(root string) Store { return files{root} }

// proofFile is the proof sidecar of a valid file-mode ID.
func proofFile(root, id string) (string, error) {
	if !backlog.ValidID(id, backlog.FileIDDigits) {
		return "", exitcode.Errf(exitcode.ExitUsage, "ID must look like B07, F12 or T03, got %q", id)
	}
	return backlog.ProofPath(root, id), nil
}

func (s files) Update(id string, fn func(string) (string, bool, error)) error {
	path, err := proofFile(s.root, id)
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
		if rerr != nil { // missing: start the sidecar, from the legacy rows when the detail file has any
			content = fmt.Sprintf("# %s: %s\n", id, title)
			legacy, _, lerr := backlog.ProofText(s.root, id)
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
	path, err := proofFile(s.root, id)
	if err != nil {
		return "", false, err
	}
	content, ok, rerr := backlog.ProofText(s.root, id)
	if rerr != nil {
		return "", false, exitcode.Errf(exitcode.ExitInternal, "cannot read %s: %v", path, rerr)
	}
	return content, ok, nil
}
