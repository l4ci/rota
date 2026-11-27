package land

import (
	"errors"
	"fmt"
	"strings"

	"github.com/l4ci/rota/internal/git"
)

// Kind is what a MergeLocal error means to a caller.
type Kind int

// Failure kinds.
const (
	// FailOther is an error MergeLocal does not own (ErrUnpinned, a bare runner error).
	FailOther Kind = iota
	// FailConflict is a real conflict.
	FailConflict
	// FailMerge is a merge git refused for another reason.
	FailMerge
	// FailCleanup is a failed merge whose abort could not be confirmed.
	FailCleanup
)

// Failure is a MergeLocal error classified once, for every caller.
type Failure struct {
	Kind Kind
	Code int
	Out  string
	// Paths are the conflicted paths of a FailConflict, byte-exact: match on
	// them, and show them through git.DisplayPaths.
	Paths []string
	Err   error
}

// Classify maps a MergeLocal error to a Failure. A CleanupError wins over the
// failure it wraps, even a conflict, and even when something wraps the
// CleanupError. Deliberate: an unconfirmed abort leaves the merge tree dirty,
// so "conflict, send it back to the slot" would be wrong advice; the caller
// must report the recovery failure instead. Pinned by TestClassifyRefusal.
func Classify(err error) Failure {
	f := Failure{Err: err}
	var me *MergeError
	var ce *ConflictError
	switch {
	case errors.As(err, new(*CleanupError)):
		f.Kind = FailCleanup
	case errors.As(err, &ce):
		f.Kind, f.Out, f.Paths = FailConflict, ce.Out, ce.Paths
	case errors.As(err, &me):
		f.Kind, f.Code, f.Out = FailMerge, me.Code, me.Out
	}
	return f
}

// Words is how a caller words a failed merge.
type Words struct {
	// Failed leads a failure message; the exit code and output follow it.
	Failed string
	// Conflict and ConflictHint word a real conflict; "{paths}" in either
	// becomes the comma-joined conflicted paths.
	Conflict, ConflictHint string
}

// Refusal is the message and hint a caller returns for f. Only a real conflict
// is called one: anything else (no committer identity, a hook, a locked index)
// is reported in git's own words, so it is not mistaken for work to resolve
// with the slot.
func (f Failure) Refusal(w Words) (msg, hint string) {
	switch f.Kind {
	case FailCleanup:
		return fmt.Sprintf("%s: %s", w.Failed, f.Err), ""
	case FailConflict:
		paths := ConflictList(f.Paths)
		sub := func(s string) string { return strings.ReplaceAll(s, "{paths}", paths) }
		return sub(w.Conflict), sub(w.ConflictHint)
	case FailMerge:
		return fmt.Sprintf("%s (exit %d): %s", w.Failed, f.Code, strings.TrimSpace(f.Out)), ""
	}
	return fmt.Sprintf("%s (exit 127): %s", w.Failed, f.Err), ""
}

// ConflictList words a conflict's paths for a message. The list is empty when
// git held no unmerged path or could not be asked.
func ConflictList(paths []string) string {
	if len(paths) == 0 {
		return "(see git merge output)"
	}
	return strings.Join(git.DisplayPaths(paths), ", ")
}
