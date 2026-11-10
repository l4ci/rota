package pidlive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
)

// Record is what a marker document must tell: the pid that owns it.
// Callers keep their own record types and add this one method.
type Record interface{ OwnerPID() int }

// Codec turns a record into marker bytes and back. Decode reports false for a
// body that is not a record of this kind.
type Codec[T Record] struct {
	Decode func([]byte) (T, bool)
	Encode func(T) ([]byte, error)
}

// JSON is the codec of a record kept as indented JSON with a trailing newline.
func JSON[T Record]() Codec[T] {
	return Codec[T]{
		Decode: func(b []byte) (T, bool) {
			var v T
			return v, json.Unmarshal(b, &v) == nil
		},
		Encode: func(v T) ([]byte, error) {
			b, err := jsonx.Marshal(v)
			return append(b, '\n'), err
		},
	}
}

// Stamp is a process identity: the pid and, where the machine gives it, the
// start time that tells this process from a later one reusing the pid.
type Stamp struct {
	PID   int
	Start uint64
}

// OwnerPID implements Record.
func (s Stamp) OwnerPID() int { return s.PID }

// StampOf is the identity of pid as src reads it. Start is 0 when src cannot
// read it.
func StampOf(src Source, pid int) Stamp {
	_, st, _ := src.Stat(pid)
	return Stamp{PID: pid, Start: st}
}

// Live reports whether the process s names is still that process.
func (s Stamp) Live(src Source) bool { return Running(src, s.PID, s.Start) }

// Text is the codec of the plain-text marker body "pid start\n". A marker
// without a start time ("pid\n": older rota, or no /proc) still decodes, with
// Start 0.
func Text() Codec[Stamp] {
	return Codec[Stamp]{
		Decode: func(b []byte) (Stamp, bool) {
			f := strings.Fields(string(b))
			if len(f) == 0 {
				return Stamp{}, false
			}
			pid, err := strconv.Atoi(f[0])
			if err != nil {
				return Stamp{}, false
			}
			s := Stamp{PID: pid}
			if len(f) > 1 {
				s.Start, _ = strconv.ParseUint(f[1], 10, 64)
			}
			return s, true
		},
		Encode: func(s Stamp) ([]byte, error) {
			if s.Start == 0 {
				return []byte(fmt.Sprintf("%d\n", s.PID)), nil
			}
			return []byte(fmt.Sprintf("%d %d\n", s.PID, s.Start)), nil
		},
	}
}

// Marker is one pid-owned marker file: the record of a running process, kept
// so another process can tell it is there. It owns the whole lifecycle: write
// as owner, read what is live, remove only if still the caller's.
type Marker[T Record] struct {
	Path  string
	Codec Codec[T]
}

// Read loads the record; ok is false when the file is missing, does not
// decode or names no pid.
func (m Marker[T]) Read() (T, bool) {
	var zero T
	b, err := os.ReadFile(m.Path)
	if err != nil {
		return zero, false
	}
	v, ok := m.Codec.Decode(b)
	if !ok || v.OwnerPID() <= 0 {
		return zero, false
	}
	return v, true
}

// ReadLive is Read for a record whose process live still vouches for: a
// marker left by a dead process, or a reused pid, reads as absent.
func (m Marker[T]) ReadLive(live func(T) bool) (T, bool) {
	v, ok := m.Read()
	if !ok || !live(v) {
		var zero T
		return zero, false
	}
	return v, true
}

// Write records v under the marker's lock. guard, when non-nil, runs under the
// lock first and its error aborts the write.
func (m Marker[T]) Write(v T, guard func() error) error {
	b, err := m.Codec.Encode(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.Path), 0o777); err != nil {
		return err
	}
	return fsio.Locked(m.Path, fsio.LockTimeout, func() error {
		if guard != nil {
			if err := guard(); err != nil {
				return err
			}
		}
		return fsio.WriteFileAtomic(m.Path, b)
	})
}

// Claim writes v unless another live process holds the marker: that record is
// returned with held true and nothing is written. The check and the write
// share one lock, so two claimants starting together cannot both win. A marker
// of a dead process, an unreadable one or one already v's own is replaced.
func (m Marker[T]) Claim(v T, live func(T) bool) (other T, held bool, err error) {
	err = m.Write(v, func() error {
		if cur, ok := m.ReadLive(live); ok && cur.OwnerPID() != v.OwnerPID() {
			other, held = cur, true
			return errHeld
		}
		return nil
	})
	if held {
		return other, true, nil
	}
	return other, false, err
}

var errHeld = fmt.Errorf("marker held by another process")

// Remove deletes the marker when it is still pid's, so a successor that
// replaced a dead owner's record keeps its own. A missing file is not an error.
func (m Marker[T]) Remove(pid int) error {
	return fsio.RemoveMarker(m.Path, func(b []byte) bool {
		v, ok := m.Codec.Decode(b)
		return ok && v.OwnerPID() == pid
	})
}
