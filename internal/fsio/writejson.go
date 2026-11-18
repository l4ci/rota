package fsio

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/l4ci/rota/internal/jsonx"
)

// WriteJSONAtomic writes v as json.dumps(v, indent=2) plus a newline.
func WriteJSONAtomic(path string, v any) error {
	data, err := jsonx.Marshal(v)
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, append(data, '\n'))
}

// ErrNotLanded is wrapped by the error a verified write returns when the file
// read back is not what was written.
var ErrNotLanded = errors.New("write did not land")

// writeJSONVerified is WriteJSONAtomic plus a re-read: a success exit with
// nothing (or something else) on disk is an error, not a success (#579).
func writeJSONVerified(path string, v any) error {
	data, err := jsonx.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := WriteFileAtomic(path, data); err != nil {
		return err
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: %s unreadable after writing: %v", ErrNotLanded, path, err)
	}
	if !bytes.Equal(got, data) {
		return fmt.Errorf("%w: %s differs from what was written", ErrNotLanded, path)
	}
	return nil
}

// UpdateJSON is a locked read-modify-write: it loads path (def when missing
// or corrupt), lets mutate return the new value, and writes it atomically.
func UpdateJSON(path string, def any, mutate func(any) (any, error)) error {
	return updateJSON(path, def, LockTimeout, mutate)
}

// UpdateJSONStrict is UpdateJSON for a file whose loss is destructive: a file
// that exists but does not parse is an error, where UpdateJSON would start
// from def and overwrite it. A missing file still starts from def. The write
// is re-read before it counts as done.
func UpdateJSONStrict(path string, def any, mutate func(any) (any, error)) error {
	return Locked(path, LockTimeout, func() error {
		cur := def
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			if cur, err = jsonx.Decode(raw); err != nil {
				return fmt.Errorf("%w: %s does not parse (%v); fix or remove it, rota will not overwrite it", ErrUnreadable, path, err)
			}
		case !errors.Is(err, os.ErrNotExist):
			return fmt.Errorf("%w: %s: %v", ErrUnreadable, path, err)
		}
		v, err := mutate(cur)
		if err != nil {
			return err
		}
		return writeJSONVerified(path, v)
	})
}

// updateJSON is UpdateJSON with an explicit lock budget, so a test can give a
// heavily contended lock more time than production allows.
func updateJSON(path string, def any, timeout time.Duration, mutate func(any) (any, error)) error {
	return Locked(path, timeout, func() error {
		v, err := mutate(LoadJSON(path, def))
		if err != nil {
			return err
		}
		return writeJSONVerified(path, v)
	})
}
