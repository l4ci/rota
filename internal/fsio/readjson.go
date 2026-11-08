package fsio

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// ReadJSON decodes the JSON state file at path into a T. A missing file is
// found=false with no error; an empty or malformed one is an error wrapping
// ErrUnreadable that names path; any other read failure is returned as is.
// The caller keeps its own validation of the decoded value, and decides what
// an unreadable file means for it.
func ReadJSON[T any](path string) (v T, found bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		var zero T
		return zero, false, fmt.Errorf("%w: %s: %v", ErrUnreadable, path, err)
	}
	return v, true, nil
}
