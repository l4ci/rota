package worker

import (
	"errors"
	"testing"

	"github.com/l4ci/rota/internal/exitcode"
)

func TestDispatchRefusesExternal(t *testing.T) {
	dir := newProject(t, `{}`)
	goInit(t, dir, InitOpts{Slots: 1, Base: "main"})
	if err := RegisterExternal(dir, "ext-1", "codex/12-x", "", "main", "12", ""); err != nil {
		t.Fatal(err)
	}
	s := LoadRegistry(dir).Slot("ext-1")
	if !s.IsExternal() || s.Handle() != "" || s.Task() != "12" || s.Kind() != KindExternal {
		t.Fatalf("slot: %v", s.Raw())
	}
	for _, relay := range []bool{false, true} {
		f := tmuxFake()
		_, err := envWith(f).Dispatch(bg, dir, DispatchOpts{Slot: "ext-1", BodyFile: writeBrief(t, "go\n"), Task: "#12", Relay: relay})
		var xe *exitcode.Error
		if !errors.As(err, &xe) || xe.Exit != exitcode.ExitRefused || xe.Message != "external slot has no host" {
			t.Fatalf("relay=%v: %v", relay, err)
		}
		if bd, ok := xe.Data.(BlockData); !ok || bd.BlockedBy != "host" {
			t.Errorf("relay=%v data: %#v", relay, xe.Data)
		}
	}
}
