package host

import (
	"context"
	"errors"
	"testing"
)

// New must not hand a tmux host to Solo or an unknown kind: the caller bug
// would surface later as a tmux error, far from its cause.
func TestNewRefusesSoloAndUnknownKinds(t *testing.T) {
	for _, kind := range []Kind{Solo, "bogus", ""} {
		t.Run(string(kind), func(t *testing.T) {
			h := New(kind, deps(&fake{}, nil, &clock{}))
			if h.Kind() == Tmux {
				t.Fatalf("New(%q).Kind() = tmux, want no tmux fallback", kind)
			}
			if _, ok := h.(*tmux); ok {
				t.Fatalf("New(%q) returned a tmux adapter", kind)
			}
			if err := h.Require(); err == nil {
				t.Fatalf("New(%q).Require() = nil, want a refusal", kind)
			}
			handle, err := h.Spawn(context.Background(), SpawnOpts{Slot: "ben"})
			if err == nil || handle != "" {
				t.Fatalf("New(%q).Spawn = %q, %v; want a refusal", kind, handle, err)
			}
			if !errors.Is(err, ErrUnknownKind) {
				t.Fatalf("Spawn error = %v, want ErrUnknownKind", err)
			}
		})
	}
}
