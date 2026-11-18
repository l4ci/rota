package fsio

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/l4ci/rota/internal/jsonx"
)

// ErrUnreadable is wrapped by the error UpdateJSONStrict returns for a file
// that exists but cannot be read or parsed.
var ErrUnreadable = errors.New("state file unreadable")

// LoadJSON reads a JSON document, or returns def when the file is missing
// or does not parse, like hvlib_io.load_json. Objects retain key order in
// jsonx values; any read or decode error returns def unchanged. Use ReadJSON[T]
// for typed decoding with explicit missing-file and parse-error results.
func LoadJSON(path string, def any) any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	v, err := jsonx.Decode(raw)
	if err != nil {
		return def
	}
	return v
}

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
