package backlog

import (
	"errors"
	"fmt"
)

// Sentinels for the write side. ErrNotFound and ErrWrongBackend live in
// model.go. A caller maps them to exit codes with errors.Is / errors.As;
// backlog never imports internal/cli.
var (
	// ErrInvalid is bad input: an unknown kind, tag or field, an empty value.
	// The verb contract answers these with a usage error (exit 2).
	ErrInvalid = errors.New("invalid input")
	// ErrProofMissing: a `done` close with no proof row recorded.
	ErrProofMissing = errors.New("proof missing")
	// ErrClosed: a field write on a completed or archived item.
	ErrClosed = errors.New("item is closed")
	// ErrNotPorted: the issue backend has no implementation of the verb yet.
	ErrNotPorted = errors.New("not ported yet")
)

// kindErr carries a plain message and answers errors.Is for one sentinel.
type kindErr struct {
	kind error
	msg  string
}

func (e *kindErr) Error() string { return e.msg }
func (e *kindErr) Unwrap() error { return e.kind }

func errf(kind error, format string, a ...any) error {
	return &kindErr{kind: kind, msg: fmt.Sprintf(format, a...)}
}

// RefusedError is a mutating verb declining to break an invariant. BlockedBy
// names the invariant in a word or phrase for the exit-4 failure data.
type RefusedError struct {
	BlockedBy string
	Msg       string
	Hint      string // optional next step
	Err       error  // optional sentinel, for errors.Is
}

func (e *RefusedError) Error() string { return e.Msg }
func (e *RefusedError) Unwrap() error { return e.Err }

func refused(blockedBy string, sentinel error, format string, a ...any) error {
	return &RefusedError{BlockedBy: blockedBy, Msg: fmt.Sprintf(format, a...), Err: sentinel}
}

// ActiveError: an item the removal would drop is active on a branch.
type ActiveError struct {
	ID, Branch string
}

func (e *ActiveError) Error() string {
	return fmt.Sprintf("[%s] is active on branch %s; end that stream first", e.ID, e.Branch)
}
