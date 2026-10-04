// Package artifacttest holds in-memory stand-ins for the issue-side seams of
// package artifact, so a kind's tests can run its note store without a tracker.
package artifacttest

// Notes is an in-memory artifact.Notes keyed by item and kind.
type Notes struct{ M map[[2]string]string }

// NewNotes is an empty Notes.
func NewNotes() *Notes { return &Notes{M: map[[2]string]string{}} }

func (n *Notes) NoteGet(ref, kind string) (string, bool, error) {
	t, ok := n.M[[2]string{ref, kind}]
	return t, ok, nil
}

// NotePut stores text; changed is false when the note already reads so.
func (n *Notes) NotePut(ref, kind, text string) (bool, error) {
	k := [2]string{ref, kind}
	if old, ok := n.M[k]; ok && old == text {
		return false, nil
	}
	n.M[k] = text
	return true, nil
}

func (n *Notes) NoteRm(ref, kind string) (bool, error) {
	k := [2]string{ref, kind}
	_, ok := n.M[k]
	delete(n.M, k)
	return ok, nil
}
