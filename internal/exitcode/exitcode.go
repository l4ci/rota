// Package exitcode holds the exit-code table and the one exit-coded error
// every verb-facing package returns. It imports nothing from rota, so cli,
// artifact and worker can all share it.
package exitcode

import (
	"errors"
	"fmt"
)

// Exit codes; see docs/contributing/contract/cli-conventions.md, Exit codes.
const (
	ExitOK             = 0
	ExitFailed         = 1
	ExitUsage          = 2
	ExitResolution     = 3
	ExitRefused        = 4
	ExitUnavailable    = 5
	ExitRetry          = 6
	ExitInternal       = 70
	ExitNotImplemented = 71
)

var codeNames = map[int]string{
	ExitFailed:         "failed",
	ExitUsage:          "usage",
	ExitResolution:     "resolution",
	ExitRefused:        "refused",
	ExitUnavailable:    "unavailable",
	ExitRetry:          "retry",
	ExitInternal:       "internal",
	ExitNotImplemented: "not_implemented",
}

// CodeName is the error.code string for an exit code.
func CodeName(exit int) string { return codeNames[exit] }

// Error is a verb failure with its exit code, an optional hint line and
// optional failure data a verb may attach on exit 1 and 4.
type Error struct {
	Exit    int
	Message string
	Hint    string
	Data    any
	// Cause is the error this one was translated from, kept so a caller can
	// still reach its typed detail (a failed saga stage) with errors.As.
	Cause error
}

func (e *Error) Error() string { return e.Message }

// Unwrap exposes Cause.
func (e *Error) Unwrap() error { return e.Cause }

// Errf builds an Error.
func Errf(exit int, format string, a ...any) *Error {
	return &Error{Exit: exit, Message: fmt.Sprintf(format, a...)}
}

// WithHint returns e with a hint line added.
func (e *Error) WithHint(hint string) *Error { e.Hint = hint; return e }

// DataOf reads the failure data of the exit-coded error err wraps as a T. It
// is the one typed accessor for refusal data: a caller names the type it
// expects instead of asserting on Error.Data itself.
func DataOf[T any](err error) (T, bool) {
	var e *Error
	if errors.As(err, &e) {
		d, ok := e.Data.(T)
		return d, ok
	}
	var zero T
	return zero, false
}
