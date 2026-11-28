package fsio

import (
	"errors"
	"os"

	"github.com/l4ci/rota/internal/pystr"
)

// ReadText reads a text file the way Python's Path.read_text does: universal
// newlines, so "\r\n" and a lone "\r" both become "\n". A missing file
// returns the os error. Use it for every markdown or text state file that is
// parsed or rewritten, so a CRLF file does not end up with mixed endings.
func ReadText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return pystr.UniversalNewlines(string(b)), nil
}

// ReadUserText is ReadText for a file the user keeps: any failure but a
// missing file is marked Unreadable. A missing file's error is returned as is.
func ReadUserText(path string) (string, error) {
	t, err := ReadText(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", Unreadable(err)
	}
	return t, err
}
