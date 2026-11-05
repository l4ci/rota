package harness

import "testing"

func TestAdapterInputSyntax(t *testing.T) {
	if got := ComposerMarkers(); got != "❯›" {
		t.Fatalf("composer glyphs = %q", got)
	}
	syntax := ResolveInputSyntax(Codex)
	if syntax.Composer != "›" || len(syntax.QueueHeadings) != 3 {
		t.Fatalf("checked syntax = %+v", syntax)
	}
	// A host can keep or amend its own view without changing another capture.
	syntax.QueueHeadings[0] = "modified"
	if got := ResolveInputSyntax(Codex).QueueHeadings[0]; got != "Queued follow-up inputs" {
		t.Fatalf("adapter syntax was mutated: %q", got)
	}
}

func TestInputSyntaxUsesRecordedKind(t *testing.T) {
	for _, tc := range []struct{ app, composer string }{
		{"codex", "›"}, {"claude-code", "❯"},
	} {
		kind, ok := CoreWorkerKind(tc.app)
		if !ok || ResolveInputSyntax(kind).Composer != tc.composer {
			t.Errorf("native app %q resolved to kind %q, found=%v", tc.app, kind, ok)
		}
	}
	if _, ok := CoreWorkerKind("unknown"); ok {
		t.Error("unknown native app resolved to a worker kind")
	}
	if got := ResolveInputSyntax("unknown").Composer; got != "" {
		t.Errorf("unknown kind inherited composer %q", got)
	}
	if ResolveInputSyntax(Claude).IsPlaceholder("Ask Codex to do anything") {
		t.Error("Claude inherited a Codex placeholder")
	}
}
