package cli

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/exitmap"
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
	ExitInterrupted    = exitcode.ExitInterrupted
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

// configFail translates the config errors in the classes c into their exit
// codes (see internal/exitmap); anything else passes through unchanged. It is
// the one place the cli decides what a config error exits with.
func configFail(err error, c exitmap.Class) error {
	_, out := exitmap.Translate(err, exitmap.Options{Classes: c})
	return out
}

// asError maps any error to an *Error: lock timeouts become retry, an unreadable state file is unavailable
// (both through exitmap), and anything unclassified is an internal error.
func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	if _, out := exitmap.Translate(err, exitmap.Options{Classes: exitmap.Lock | exitmap.Unreadable}); out != nil {
		if errors.As(out, &e) {
			return e
		}
	}
	return &Error{Exit: ExitInternal, Message: err.Error(), Hint: "this is a bug in rota; please report it"}
}

// signalSeen is set when a verb's signal context saw SIGINT or SIGTERM. It
// lets interrupted tell that cancellation from one rota raised itself.
var signalSeen atomic.Bool

// interrupted turns err into the exit-130 error when err is a cancellation
// that SIGINT or SIGTERM caused: ctx is the invocation's signal context, and
// signalSeen covers the verb-local contexts workerContext hands out. Any other
// context.Canceled is unclassified and stays an internal error (exit 70). A
// verb's own *Error keeps its class.
func interrupted(ctx context.Context, err error) error {
	var e *Error
	if err == nil || errors.As(err, &e) || !errors.Is(err, context.Canceled) {
		return err
	}
	if ctx.Err() == nil && !signalSeen.Load() {
		return err
	}
	return &Error{Exit: ExitInterrupted, Message: "interrupted"}
}
