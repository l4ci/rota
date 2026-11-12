package fsio

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AppendLine appends line plus a newline to path under the file's lock and
// fsyncs it before returning, so a returned nil means the whole line is on
// disk. The file is created with mode when absent; its directory is not.
func AppendLine(path string, line []byte, mode os.FileMode) error {
	return Locked(path, LockTimeout, func() error {
		return appendLocked(path, append(line[:len(line):len(line)], '\n'), mode)
	})
}

// AppendText appends text verbatim (no newline added) to path under the
// file's lock and fsyncs it. The file is created with mode when absent.
func AppendText(path, text string, mode os.FileMode) error {
	return Locked(path, LockTimeout, func() error { return appendLocked(path, []byte(text), mode) })
}

// AppendLineUnique appends line plus a newline to path unless a line equal to
// it is already there, and reports whether it appended. The check and the
// append happen under one lock. A file whose last line lacks a newline gets
// one first. The file is created with mode when absent and its directory is
// created as needed.
func AppendLineUnique(path, line string, mode os.FileMode) (added bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return false, err
	}
	err = Locked(path, LockTimeout, func() error {
		have, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, l := range strings.Split(string(have), "\n") {
			if l == line {
				return nil
			}
		}
		out := line + "\n"
		if len(have) > 0 && !bytes.HasSuffix(have, []byte("\n")) {
			out = "\n" + out
		}
		if err := appendLocked(path, []byte(out), mode); err != nil {
			return err
		}
		added = true
		return nil
	})
	return added, err
}

// appendLocked writes data to path with O_APPEND and fsyncs; the caller holds
// the lock.
func appendLocked(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// LinePolicy says what ReadLines does with a line decode rejects.
type LinePolicy int

const (
	// Lenient counts a rejected line as skipped and reads on.
	Lenient LinePolicy = iota
	// Strict stops at a rejected line and returns a *LineError.
	Strict
)

// LineError is a Strict read stopped by a line decode rejected.
type LineError struct {
	Line int // 1-based
	Err  error
}

func (e *LineError) Error() string { return fmt.Sprintf("line %d: %v", e.Line, e.Err) }
func (e *LineError) Unwrap() error { return e.Err }

// ReadLines passes each non-blank line of path, in order, to decode. A missing
// file returns the os error untouched, so callers choose whether it means
// empty (errors.Is(err, os.ErrNotExist)). A line decode rejects is counted in
// skipped under Lenient and ends the read under Strict. Line length is
// unbounded.
func ReadLines(path string, policy LinePolicy, decode func(line []byte) error) (skipped int, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for i, l := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(l)) == 0 {
			continue
		}
		if derr := decode(l); derr != nil {
			if policy == Strict {
				return skipped, &LineError{Line: i + 1, Err: derr}
			}
			skipped++
		}
	}
	return skipped, nil
}

// RewriteLines drops from path the lines keep rejects, under the file's lock,
// and returns how many it dropped. Empty lines are discarded without being
// counted. The file is rewritten atomically and only when something was
// dropped; a missing file drops nothing.
func RewriteLines(path string, keep func(line []byte) bool) (dropped int, err error) {
	err = Locked(path, LockTimeout, func() error {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		var kept bytes.Buffer
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			if !keep(line) {
				dropped++
				continue
			}
			kept.Write(line)
			kept.WriteByte('\n')
		}
		if dropped == 0 {
			return nil
		}
		return WriteFileAtomic(path, kept.Bytes())
	})
	return dropped, err
}
