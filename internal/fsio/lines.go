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
// disk. A file whose last line lacks a newline gets one first. The file is
// created with mode when absent; its directory is not.
func AppendLine(path string, line []byte, mode os.FileMode) error {
	return Locked(path, LockTimeout, func() error {
		return appendLocked(path, line, true, mode)
	})
}

// AppendText appends text verbatim (no newline added) to path under the
// file's lock and fsyncs it. The file is created with mode when absent.
func AppendText(path, text string, mode os.FileMode) error {
	return Locked(path, LockTimeout, func() error { return appendLocked(path, []byte(text), false, mode) })
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
		if err := appendLocked(path, []byte(line), true, mode); err != nil {
			return err
		}
		added = true
		return nil
	})
	return added, err
}

// appendLocked writes data to path with O_APPEND and fsyncs; the caller holds
// the lock. With record set, data is one line and is framed by frameLine;
// otherwise it goes out verbatim.
func appendLocked(path string, data []byte, record bool, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, mode)
	if err != nil {
		return err
	}
	if record {
		if data, err = frameLine(f, data); err != nil {
			f.Close()
			return err
		}
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

// frameLine returns line plus a newline, led by another when f ends mid-line
// (a torn append), so the new record is not fused to the remains.
func frameLine(f *os.File, line []byte) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(line)+2)
	if n := fi.Size(); n > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], n-1); err != nil {
			return nil, err
		}
		if last[0] != '\n' {
			out = append(out, '\n')
		}
	}
	return append(append(out, line...), '\n'), nil
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
