package host

import (
	"errors"
	"fmt"
)

// ErrMaybeSubmitted classifies a failed send whose delivery is unknown: the
// host may already have handed the text to the session (an RPC failing after
// the prompt was accepted, a branch check after pickup). A caller must not
// tell the operator the brief was never sent, and must not paste a second copy.
// It is the counterpart of ErrNotSubmitted, which is the safe-to-resend class.
var ErrMaybeSubmitted = errors.New("brief may have been submitted")

// classifiedSend carries a failure beside its delivery class; the original
// message stays what the operator reads.
type classifiedSend struct{ err, class error }

func (e *classifiedSend) Error() string   { return e.err.Error() }
func (e *classifiedSend) Unwrap() []error { return []error{e.err, e.class} }

// classifySend gives every Send failure a delivery class. A failure that
// already states one (not submitted, queued, accepted, dialog, human draft) is
// left alone; any other failure is wrapped as ErrMaybeSubmitted.
func classifySend(err error) error {
	for _, known := range []error{nil, ErrNotSubmitted, ErrMaybeSubmitted, ErrQueued, ErrSubmitted, ErrDialogOpen, ErrDraftOnPrompt} {
		if errors.Is(err, known) {
			return err
		}
	}
	return &classifiedSend{err: err, class: ErrMaybeSubmitted}
}

// readBrief reads the brief file a Send delivers. A failed read means nothing
// was sent, so every adapter reports it as ErrNotSubmitted.
func (d *Deps) readBrief(file string) (string, error) {
	text, err := d.readFile(file)
	if err != nil {
		return "", fmt.Errorf("%w: cannot read brief: %v", ErrNotSubmitted, err)
	}
	return text, nil
}
