package harness

import "testing"

func TestAdapterInputSyntax(t *testing.T) {
	if got := ComposerMarkers(); got != "❯›" {
		t.Fatalf("composer glyphs = %q", got)
	}
	syntax := CheckedComposerSyntax()
	if syntax.Composer != "›" || len(syntax.QueueHeadings) != 3 {
		t.Fatalf("checked syntax = %+v", syntax)
	}
	// A host can keep or amend its own view without changing another capture.
	syntax.QueueHeadings[0] = "modified"
	if got := CheckedComposerSyntax().QueueHeadings[0]; got != "Queued follow-up inputs" {
		t.Fatalf("adapter syntax was mutated: %q", got)
	}
}
