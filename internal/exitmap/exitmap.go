// Package exitmap is the one translation from domain errors (backlog
// sentinels and refusals, tracker.Error, a busy lock) to an exit-coded error.
// Verb families differ in what they recognise, what an unclassified error
// becomes and whether a read-only verb may refuse; those differences are the
// Options, not separate switches.
package exitmap

import (
	"errors"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/blockedby"
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
	// Tracker: any tracker.Error, by its kind (Kind.Exit), and a
	// tracker.UnknownForgeError (exit 4, blockedBy forge).
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
	// ConfigUsage: a malformed or unknown key, a bad value or a project-owned
	// key (exit 2).
	ConfigUsage
	// ConfigInternal: a config file that is not a JSON object (any layer) or
	// is unreadable (exit 70).
	ConfigInternal
	// ConfigCorrupt: config.ErrCorrupt, a config file that is not valid JSON
	// (exit 70).
	ConfigCorrupt
	// ConfigRefusal: a migration conflict between old and new names (exit 4,
	// with blockedBy data and the conflicts).
	ConfigRefusal

	// Config is every config class.
	Config = ConfigUsage | ConfigInternal | ConfigCorrupt | ConfigRefusal

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
// return. It never mutates supplied errors. Existing exit classifications pass
// through by identity, except read-only exit-4 demotion, which owns a copy of
// the classification. For classified results, errors.As exposes the effective
// *exitcode.Error; the original chain remains inspectable. Unclassified errors
// under a zero Default pass through unchanged. nil in, nil out.
func Translate(err error, o Options) (data any, out error) {
	if err == nil {
		return nil, nil
	}
	var e *exitcode.Error
	if errors.As(err, &e) {
		if o.ReadOnly && e.Exit == exitcode.ExitRefused {
			owned := *e
			owned.Exit = exitcode.ExitFailed
			return nil, &demotedError{original: err, classification: owned}
		}
		return nil, err
	}
	data, out = translate(err, o)
	if out != err && errors.As(out, &e) && e.Cause == nil {
		e.Cause = err
	}
	if o.ReadOnly && errors.As(out, &e) && e.Exit == exitcode.ExitRefused {
		e.Exit = exitcode.ExitFailed
	}
	return data, out
}

// demotedError owns the effective classification while preserving the supplied
// error's text and complete chain, including any wrappers outside its exit code.
type demotedError struct {
	original       error
	classification exitcode.Error
}

func (e *demotedError) Error() string { return e.original.Error() }
func (e *demotedError) Unwrap() error { return e.original }

func (e *demotedError) As(target any) bool {
	if coded, ok := target.(**exitcode.Error); ok {
		*coded = &e.classification
		return true
	}
	return false
}

func translate(err error, o Options) (any, error) {
	if d, out, ok := translateConfig(err, o); ok {
		return d, out
	}
	var te *tracker.Error
	var ex interface{ Exit() int }
	var ref *backlog.RefusedError
	var act *backlog.ActiveError
	has := func(c Class) bool { return o.Classes&c != 0 }
	var ufe *tracker.UnknownForgeError
	switch {
	case has(Tracker) && errors.As(err, &ufe):
		return jsonx.Obj("blockedBy", blockedby.Forge, "changed", false),
			&exitcode.Error{Exit: exitcode.ExitRefused, Message: err.Error(), Hint: tracker.UnknownForgeHint}
	case has(Tracker) && errors.As(err, &te):
		return nil, &exitcode.Error{Exit: te.Kind.Exit(), Message: te.Message}
	case has(Exiter) && errors.As(err, &ex):
		return nil, &exitcode.Error{Exit: ex.Exit(), Message: err.Error()}
	case has(Refusal) && errors.As(err, &ref):
		return jsonx.Obj("blockedBy", ref.BlockedBy, "changed", false),
			&exitcode.Error{Exit: exitcode.ExitRefused, Message: ref.Msg, Hint: ref.Hint}
	case has(Refusal) && errors.As(err, &act):
		return jsonx.Obj("blockedBy", blockedby.Active, "id", act.ID, "activeBranch", act.Branch, "changed", false),
			&exitcode.Error{Exit: exitcode.ExitRefused, Message: act.Error(), Hint: "end the stream first: rota status rm " + act.Branch}
	case has(Refusal) && errors.Is(err, backlog.ErrWrongBackend):
		return jsonx.Obj("blockedBy", blockedby.Backend, "changed", false),
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
