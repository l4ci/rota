// Package fsio holds rota's file primitives. Atomic writes, JSON bytes and
// sidecar advisory locks match bin/hvlib_io.py, so rota and the old helpers
// can share state files (docs/contributing/contract/cli-conventions.md, Writes).
//
// atomic.go provides durable byte writes and file moves; readtext.go provides
// Python-compatible text reads; directory.go provides directory and glob helpers.
// readjson.go offers LoadJSON for ordered jsonx values with a fallback default,
// or ReadJSON[T] for typed values with explicit read and parse errors.
// writejson.go provides atomic JSON writes and locked read-modify-write helpers;
// choose UpdateJSONStrict when unreadable existing state must not be overwritten.
// marker.go provides guarded marker writes and ownership-checked removal.
//
// lock.go provides Locked for waits bounded by a timeout and LockedCtx when
// cancellation should also bound the wait. Both lock the data path's .lock
// sidecar, and neither interrupts the callback once the lock is held. TryLock
// instead takes a nonblocking lock directly on the supplied path; use it only
// for a dedicated lock file that will not be atomically replaced.
package fsio
