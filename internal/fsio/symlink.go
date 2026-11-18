package fsio

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// SymlinkIn returns the first symlink in the slash-separated relative path
// rel below root, or an empty string when none is present. It does not check
// root itself. Missing components are allowed; the first other filesystem
// error is returned alongside the result. The walk continues after errors so
// callers that ignore them can still detect later links. rel is walked as
// spelled, without cleaning away components.
// Callers checking only parent directories omit the final component.
func SymlinkIn(root, rel string) (string, error) {
	if rel == "." || rel == "" {
		return "", nil
	}
	cur := root
	var firstErr error
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return cur, firstErr
		}
	}
	return "", firstErr
}
