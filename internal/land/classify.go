package land

import (
	"errors"
	"fmt"
	"strings"
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
	// Paths are the conflicted paths of a FailConflict.
	Paths []string
	Err   error
}

// Classify maps a MergeLocal error to a Failure. A CleanupError wins over the
// failure it wraps, as the type documents.
func Classify(err error) Failure {
	f := Failure{Err: err}
	var me *MergeError
	var ce *ConflictError
	switch {
	case errors.As(err, new(*CleanupError)):
		f.Kind = FailCleanup
	case errors.As(err, &ce):
		f.Kind, f.Out, f.Paths = FailConflict, ce.Out, ConflictPaths(ce.Out)
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
		paths := strings.Join(f.Paths, ", ")
		sub := func(s string) string { return strings.ReplaceAll(s, "{paths}", paths) }
		return sub(w.Conflict), sub(w.ConflictHint)
	case FailMerge:
		return fmt.Sprintf("%s (exit %d): %s", w.Failed, f.Code, strings.TrimSpace(f.Out)), ""
	}
	return fmt.Sprintf("%s (exit 127): %s", w.Failed, f.Err), ""
}

// ConflictPaths lists the paths git named in a merge's "CONFLICT (...)" lines.
func ConflictPaths(out string) []string {
	var paths []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "CONFLICT") {
			continue
		}
		if i := strings.LastIndex(l, " in "); i >= 0 && strings.Contains(l, "Merge conflict") {
			paths = append(paths, strings.TrimSpace(l[i+4:]))
		} else if i := strings.Index(l, "): "); i >= 0 { // modify/delete, rename and add/add forms
			paths = append(paths, strings.TrimSpace(l[i+3:]))
		}
	}
	if len(paths) == 0 {
		return []string{"(see git merge output)"}
	}
	return paths
}
