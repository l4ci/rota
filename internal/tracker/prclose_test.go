package tracker

import (
	"context"
	"testing"
)

func TestPRClose(t *testing.T) {
	ctx := context.Background()
	gh := scriptedAdapter(t, "github", scriptedCall{name: "gh", argv: []string{"pr", "close", "7", "--comment", "lost the pick"}})
	if err := gh.PRClose(ctx, 7, "lost the pick"); err != nil {
		t.Errorf("github: %v", err)
	}
	gl := scriptedAdapter(t, "gitlab",
		scriptedCall{name: "glab", argv: []string{"mr", "note", "7", "--message", "lost the pick"}},
		scriptedCall{name: "glab", argv: []string{"mr", "close", "7"}})
	if err := gl.PRClose(ctx, 7, "lost the pick"); err != nil {
		t.Errorf("gitlab: %v", err)
	}
}

func TestPRCloseFailure(t *testing.T) {
	ctx := context.Background()
	gh := scriptedAdapter(t, "github", scriptedCall{name: "gh", argv: []string{"pr", "close", "7", "--comment", "c"}, stderr: "HTTP 422: Validation Failed", code: 1})
	if err := gh.PRClose(ctx, 7, "c"); !IsKind(err, KindFailed) {
		t.Errorf("github: %v, want KindFailed", err)
	}
	// A failed note must not close the MR unannounced.
	gl := scriptedAdapter(t, "gitlab", scriptedCall{name: "glab", argv: []string{"mr", "note", "7", "--message", "c"}, stderr: "ERROR: 500 Internal Server Error", code: 1})
	if err := gl.PRClose(ctx, 7, "c"); !IsKind(err, KindFailed) {
		t.Errorf("gitlab: %v, want KindFailed", err)
	}
}
