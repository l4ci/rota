// Package exitcode holds the exit-code table and the one exit-coded error
// every verb-facing package returns. It imports nothing from rota, so cli,
// artifact and worker can all share it.
package exitcode

import "fmt"

// Exit codes; see docs/design/5.0-cli-conventions.md, Exit codes.
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
}

func (e *Error) Error() string { return e.Message }

// Errf builds an Error.
func Errf(exit int, format string, a ...any) *Error {
	return &Error{Exit: exit, Message: fmt.Sprintf(format, a...)}
}

// WithHint returns e with a hint line added.
func (e *Error) WithHint(hint string) *Error { e.Hint = hint; return e }
