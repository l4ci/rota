package host

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// Every adapter classifies an unreadable brief file the same way: nothing was
// sent, so the class is ErrNotSubmitted and never ErrMaybeSubmitted.
func TestSendBriefReadFailureIsNotSubmittedOnEveryAdapter(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.md")
	hosts := map[string]func() (Host, string){
		"tmux": func() (Host, string) {
			return New(Tmux, deps(&fake{handler: func(string, []string) Result { return Result{} }}, nil, &clock{})), "rota:w1"
		},
		"herdr": func() (Host, string) {
			return New(Herdr, deps(&fake{handler: func(string, []string) Result { return Result{} }}, herdrEnv, &clock{})), "w9:t7"
		},
		"orca": func() (Host, string) {
			f := newOrcaFake(t)
			return New(Orca, f.deps()), f.handle(t)
		},
		"core": func() (Host, string) {
			h, _ := coreFixture(t)
			return h, "unused"
		},
	}
	for name, mk := range hosts {
		t.Run(name, func(t *testing.T) {
			h, handle := mk()
			err := h.Send(context.Background(), "w1", handle, missing)
			if !errors.Is(err, ErrNotSubmitted) || errors.Is(err, ErrMaybeSubmitted) {
				t.Fatalf("Send = %v, want ErrNotSubmitted only", err)
			}
		})
	}
}

// A failure with no stated class is classified as maybe-submitted, with its
// message intact; a stated class is left alone.
func TestClassifySend(t *testing.T) {
	boom := errors.New("boom")
	got := classifySend(boom)
	if !errors.Is(got, ErrMaybeSubmitted) || !errors.Is(got, boom) || got.Error() != "boom" {
		t.Fatalf("classifySend(boom) = %v", got)
	}
	for _, e := range []error{nil, ErrNotSubmitted, ErrQueued, ErrSubmitted, ErrDialogOpen, ErrDraftOnPrompt, got} {
		if out := classifySend(e); out != e {
			t.Errorf("classifySend(%v) rewrapped to %v", e, out)
		}
	}
}
