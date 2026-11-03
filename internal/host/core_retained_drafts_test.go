package host

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

type coreRetainedCapture struct{ Name, State, Plain, Paint string }
type coreRetainedFixture struct {
	Cols, Rows int
	Cases      []coreRetainedCapture
}

func loadCoreRetained(t *testing.T) coreRetainedFixture {
	t.Helper()
	b, e := os.ReadFile("testdata/core-retained-drafts.json")
	if e != nil {
		t.Fatal(e)
	}
	var fixture coreRetainedFixture
	if e = json.Unmarshal(b, &fixture); e != nil {
		t.Fatal(e)
	}
	return fixture
}
func (f coreRetainedFixture) capture(t *testing.T, name string) coreRetainedCapture {
	t.Helper()
	for _, c := range f.Cases {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("missing captured case %s", name)
	return coreRetainedCapture{}
}
func setCoreRetained(f *coreFake, fixture coreRetainedFixture, capture coreRetainedCapture) {
	f.cols, f.rows = fixture.Cols, fixture.Rows
	f.screen, f.styled, f.status = capture.Plain, capture.Paint, capture.State
}

// These are real retained-input captures, not a hand-written one-line prompt
// approximation. Native idle never authorizes pasting into a human composer.
func TestCoreRetainedDraftAdapterRefusesBeforeMutation(t *testing.T) {
	fixture := loadCoreRetained(t)
	for _, name := range []string{"leading-newline-draft", "trailing-glyph-draft", "leading-newline-cursor-on-first-row"} {
		t.Run(name, func(t *testing.T) {
			h, f := coreFixture(t)
			setCoreRetained(f, fixture, fixture.capture(t, "before"))
			handle := coreSpawn(t, h)
			draft := fixture.capture(t, name)
			setCoreRetained(f, fixture, draft)
			if f.status != "idle" {
				t.Fatal("retained draft must exercise positive native idle")
			}
			e := h.Send(context.Background(), "dana", handle, coreBrief(t, "--- ORCHESTRATOR ---\nsynthetic signed assignment\n--- ROTA-SIG "+strings.Repeat("f", 64)+" ---"))
			if !errors.Is(e, ErrDraftOnPrompt) {
				t.Errorf("real retained draft did not refuse input: %v", e)
			}
			if f.sends != 0 {
				t.Errorf("terminal.send mutated retained input %d times", f.sends)
			}
		})
	}
}
func TestCoreRetainedDraftPlainGuard(t *testing.T) {
	fixture := loadCoreRetained(t)
	for _, name := range []string{"leading-newline-draft", "trailing-glyph-draft"} {
		t.Run(name, func(t *testing.T) {
			if e := coreComposer(fixture.capture(t, name).Plain); !errors.Is(e, ErrDraftOnPrompt) {
				t.Fatalf("plain retained composer accepted: %v", e)
			}
		})
	}
}
func TestCoreRetainedDraftStyledGuard(t *testing.T) {
	fixture := loadCoreRetained(t)
	for _, name := range []string{"leading-newline-draft", "trailing-glyph-draft", "leading-newline-cursor-on-first-row"} {
		t.Run(name, func(t *testing.T) {
			capture := fixture.capture(t, name)
			if e := coreStyledComposer([]byte(capture.Paint), fixture.Cols, fixture.Rows, capture.Plain); !errors.Is(e, ErrDraftOnPrompt) {
				t.Fatalf("styled retained composer accepted: %v", e)
			}
		})
	}
}
func TestCoreRetainedCapturedEmptyAndBusyReadiness(t *testing.T) {
	fixture := loadCoreRetained(t)
	for _, name := range []string{"before", "leading-newline-cleared", "trailing-glyph-cleared"} {
		t.Run(name, func(t *testing.T) {
			capture := fixture.capture(t, name)
			if e := coreStyledComposer([]byte(capture.Paint), fixture.Cols, fixture.Rows, capture.Plain); e != nil {
				t.Fatalf("actual empty/placeholder composer refused: %v", e)
			}
			h, f := coreFixture(t)
			setCoreRetained(f, fixture, capture)
			handle := coreSpawn(t, h)
			f.status = "working"
			f.styled = strings.ReplaceAll(strings.ReplaceAll(f.styled, "state=done:app=codex", "state=working:app=codex"), "state=idle:app=codex", "state=working:app=codex")
			e := h.Send(context.Background(), "dana", handle, coreBrief(t, "--- ORCHESTRATOR ---\n[relay 73]\nsynthetic busy relay\n--- ROTA-SIG "+strings.Repeat("e", 64)+" ---"))
			if !errors.Is(e, ErrSubmitted) || f.sends != 1 {
				t.Fatalf("empty busy composer could not accept a relay: sends=%d error=%v", f.sends, e)
			}
		})
	}
}
