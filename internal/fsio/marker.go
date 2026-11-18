package fsio

import (
	"os"
	"path/filepath"

	"github.com/l4ci/rota/internal/jsonx"
)

// WriteMarker writes v as indented JSON to path under the path's lock. guard,
// when non-nil, runs under the lock first and its error aborts the write.
func WriteMarker(path string, v any, guard func() error) error {
	b, err := jsonx.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	return Locked(path, LockTimeout, func() error {
		if guard != nil {
			if err := guard(); err != nil {
				return err
			}
		}
		return WriteFileAtomic(path, append(b, '\n'))
	})
}

// RemoveMarker deletes path under the path's lock when owned says the
// contents are the caller's, so a successor's marker is never removed. A
// missing file is not an error.
func RemoveMarker(path string, owned func(data []byte) bool) error {
	return Locked(path, LockTimeout, func() error {
		b, err := os.ReadFile(path)
		if err != nil || !owned(b) {
			return nil
		}
		return os.Remove(path)
	})
}
