package fsio

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// AppendJSONL appends v as one JSON line to path, creating the file (0o600)
// and its directory (0o700) as needed. The line goes out in one O_APPEND
// write, so concurrent processes interleave whole lines, never characters.
// The logs it serves are per-developer and gitignored, hence the private modes.
func AppendJSONL(path string, v any) error {
	line, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// ReadJSONL returns the rows of a JSONL file in order and how many non-blank
// lines it skipped. A line that does not decode into T, or that valid (when
// non-nil) rejects, is torn (a crashed append) and counted instead of read, so
// one torn line loses one row. A missing file is empty. Line length is
// unbounded.
func ReadJSONL[T any](path string, valid func(*T) bool) (rows []T, skipped int, err error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	for _, l := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		var r T
		if json.Unmarshal(l, &r) != nil || (valid != nil && !valid(&r)) {
			skipped++
			continue
		}
		rows = append(rows, r)
	}
	return rows, skipped, nil
}
