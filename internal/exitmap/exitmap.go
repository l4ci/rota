// Package exitmap is the one translation from domain errors (backlog
// sentinels and refusals, tracker.Error, a busy lock) to an exit-coded error.
// Verb families differ in what they recognise, what an unclassified error
// becomes and whether a read-only verb may refuse; those differences are the
// Options, not separate switches.
package exitmap

import (
	"errors"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/exitcode"
	"github.com/l4ci/rota/internal/fsio"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

// Class is one family of domain error Translate may recognise.
type Class uint

const (
	// BacklogNotFound: backlog.ErrNotFound (exit 3).
	BacklogNotFound Class = 1 << iota
	// TrackerNotFound: a tracker.Error of KindNotFound (exit 3).
	TrackerNotFound
	// Lock: fsio.ErrLockTimeout, a busy lock (exit 6).
	Lock
	// Tracker: any tracker.Error, by its kind (Kind.Exit).
	Tracker
	// Exiter: an error with its own Exit() int.
	Exiter
	// Refusal: backlog.RefusedError, ActiveError and ErrWrongBackend (exit 4,
	// with blockedBy data).
	Refusal
	// Invalid: backlog.ErrInvalid (exit 2).
	Invalid
	// NotPorted: backlog.ErrNotPorted (exit 71).
	NotPorted

	// NotFound is both not-found kinds.
	NotFound = BacklogNotFound | TrackerNotFound
	// Backlog is every class a backlog verb sees.
	Backlog = NotFound | Tracker | Exiter | Refusal | Invalid | NotPorted
)

// Options is what differs between verb families.
type Options struct {
	// Classes the family recognises; an error outside them is unclassified.
	Classes Class
	// Default is the exit code of an unclassified error. Zero leaves it
	// unclassified: Translate returns it unchanged for the caller to handle.
	Default int
	// ReadOnly demotes any exit 4 to exit 1, with the same data: the
	// conventions forbid exit 4 on a read-only verb.
	ReadOnly bool
}

// IsNotFound is the one not-found test: the backlog sentinel, or the tracker
// saying the object does not exist.
func IsNotFound(err error) bool { return notFound(err, NotFound) }

// notFound is the not-found test restricted to the kinds in c.
func notFound(err error, c Class) bool {
	var te *tracker.Error
	return (c&BacklogNotFound != 0 && errors.Is(err, backlog.ErrNotFound)) ||
		(c&TrackerNotFound != 0 && errors.As(err, &te) && te.Kind == tracker.KindNotFound)
}

// Translate maps err onto the exit table under o. It returns the failure data
// a refusal carries (blockedBy and friends, nil otherwise) and the error to
// return: an *exitcode.Error for a classified or defaulted error, err itself
// when it already is one or is unclassified under a zero Default. nil in, nil
// out.
func Translate(err error, o Options) (data any, out error) {
	data, out = translate(err, o)
	var e *exitcode.Error
	if o.ReadOnly && errors.As(out, &e) && e.Exit == exitcode.ExitRefused {
		e.Exit = exitcode.ExitFailed
	}
	return data, out
}

func translate(err error, o Options) (any, error) {
	if err == nil {
		return nil, nil
	}
	var e *exitcode.Error
	if errors.As(err, &e) {
		return nil, err
	}
	var te *tracker.Error
	var ex interface{ Exit() int }
	var ref *backlog.RefusedError
	var act *backlog.ActiveError
	has := func(c Class) bool { return o.Classes&c != 0 }
	switch {
	case has(Tracker) && errors.As(err, &te):
		return nil, &exitcode.Error{Exit: te.Kind.Exit(), Message: te.Message}
	case has(Exiter) && errors.As(err, &ex):
		return nil, &exitcode.Error{Exit: ex.Exit(), Message: err.Error()}
	case has(Refusal) && errors.As(err, &ref):
		return jsonObj("blockedBy", ref.BlockedBy, "changed", false),
			&exitcode.Error{Exit: exitcode.ExitRefused, Message: ref.Msg, Hint: ref.Hint}
	case has(Refusal) && errors.As(err, &act):
		return jsonObj("blockedBy", "active", "id", act.ID, "activeBranch", act.Branch, "changed", false),
			&exitcode.Error{Exit: exitcode.ExitRefused, Message: act.Error(), Hint: "end the stream first: rota status rm " + act.Branch}
	case has(Refusal) && errors.Is(err, backlog.ErrWrongBackend):
		return jsonObj("blockedBy", "backend", "changed", false),
			&exitcode.Error{Exit: exitcode.ExitRefused, Message: err.Error()}
	case notFound(err, o.Classes):
		return nil, &exitcode.Error{Exit: exitcode.ExitResolution, Message: err.Error()}
	case has(Invalid) && errors.Is(err, backlog.ErrInvalid):
		return nil, &exitcode.Error{Exit: exitcode.ExitUsage, Message: err.Error()}
	case has(NotPorted) && errors.Is(err, backlog.ErrNotPorted):
		return nil, &exitcode.Error{Exit: exitcode.ExitNotImplemented, Message: err.Error()}
	case has(Lock) && errors.Is(err, fsio.ErrLockTimeout):
		return nil, &exitcode.Error{Exit: exitcode.ExitRetry, Message: err.Error()}
	}
	if o.Default == 0 {
		return nil, err
	}
	return nil, &exitcode.Error{Exit: o.Default, Message: err.Error()}
}

func jsonObj(kv ...any) *jsonx.Object {
	obj := jsonx.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		obj.Set(kv[i].(string), kv[i+1])
	}
	return obj
}
