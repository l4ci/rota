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

// classifySend gives every Send failure a delivery class. Every Host.Send
// returns through it (TestEverySendReturnsThroughClassifySend holds each
// adapter to that), so dispatch reads a class by sentinel and never meets an
// unclassified error. A failure that
// already states one (not submitted, queued, accepted, dialog, human draft) is
// left alone; any other failure is wrapped as ErrMaybeSubmitted.
//
// Today herdr and tmux return only stated classes: the paths that can fire
// after text is typed (submitTyped and confirmPickup: ErrNotSubmitted when no
// pickup was seen, ErrDialogOpen) already say so, and clearDialog, the draft
// check and the brief read run before anything is typed (ErrDialogOpen,
// ErrDraftOnPrompt, ErrNotSubmitted). The wrap is for the plain error a future
// path adds after typing. Orca and Core select ErrNotSubmitted for plain
// failures before input when no earlier Core receipt leaves delivery uncertain.
func classifySend(err error) error {
	return classifySendAs(err, ErrMaybeSubmitted)
}

// classifySendAs preserves explicit delivery evidence and supplies the class
// known at the adapter's current input boundary for an otherwise plain error.
func classifySendAs(err, fallback error) error {
	for _, known := range []error{nil, ErrNotSubmitted, ErrMaybeSubmitted, ErrQueued, ErrSubmitted, ErrDialogOpen, ErrDraftOnPrompt} {
		if errors.Is(err, known) {
			return err
		}
	}
	return &classifiedSend{err: err, class: fallback}
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
