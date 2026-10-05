package cli

import (
	"errors"
	"fmt"
	"github.com/l4ci/rota/internal/exitcode"

	"github.com/l4ci/rota/internal/fsio"
)

// Exit codes and the error type live in internal/exitcode so artifact and
// worker return the same type; these aliases keep the cli spelling.
const (
	ExitOK             = exitcode.ExitOK
	ExitFailed         = exitcode.ExitFailed
	ExitUsage          = exitcode.ExitUsage
	ExitResolution     = exitcode.ExitResolution
	ExitRefused        = exitcode.ExitRefused
	ExitUnavailable    = exitcode.ExitUnavailable
	ExitRetry          = exitcode.ExitRetry
	ExitInternal       = exitcode.ExitInternal
	ExitNotImplemented = exitcode.ExitNotImplemented
)

// CodeName is the error.code string for an exit code.
func CodeName(exit int) string { return exitcode.CodeName(exit) }

// Error is a verb failure with its exit code and an optional hint line.
type Error = exitcode.Error

func newErr(exit int, hint, format string, a ...any) *Error {
	return &Error{Exit: exit, Message: fmt.Sprintf(format, a...), Hint: hint}
}

// Failed: the verb ran and the answer is no (exit 1).
func Failed(format string, a ...any) *Error { return newErr(ExitFailed, "", format, a...) }

// Usage: bad invocation (exit 2).
func Usage(format string, a ...any) *Error { return newErr(ExitUsage, "", format, a...) }

// Resolution: something named or required could not be resolved (exit 3).
func Resolution(format string, a ...any) *Error { return newErr(ExitResolution, "", format, a...) }

// Refused: a mutating verb declined to break an invariant (exit 4).
func Refused(format string, a ...any) *Error { return newErr(ExitRefused, "", format, a...) }

// Unavailable: an external dependency is missing or failing (exit 5).
func Unavailable(format string, a ...any) *Error { return newErr(ExitUnavailable, "", format, a...) }

// Retry: a transient condition, such as a rate limit or a busy lock (exit 6).
func Retry(format string, a ...any) *Error { return newErr(ExitRetry, "", format, a...) }

// NotImplemented: the verb is in the tree but not ported yet (exit 71).
func NotImplemented(path string) *Error {
	return newErr(ExitNotImplemented, "", "%s is not ported yet", path)
}

// asError maps any error to an *Error: lock timeouts become retry, and
// anything unclassified is an internal error.
func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if errors.Is(err, fsio.ErrLockTimeout) {
		return &Error{Exit: ExitRetry, Message: err.Error()}
	}
	return &Error{Exit: ExitInternal, Message: err.Error(), Hint: "this is a bug in rota; please report it"}
}
