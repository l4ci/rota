package worker

import (
	"context"
	"testing"
	"time"
)

// #708: a gate run never leaves the invoking worktree detached. The gate only
// runs rev-parse, fetch and merge --ff-only in that tree; the merge result is
// built and verified in a detached scratch worktree.
func TestGateLeavesInvokingWorktreeOnItsBranch(t *testing.T) {
	cases := []struct {
		name string
		cfg  string
		ctx  func() (context.Context, context.CancelFunc)
	}{
		{"pass", `{"test":{"full":["true"]}}`, nil},
		{"fail", `{"test":{"full":["false"]}}`, nil},
		{"interrupted", `{"test":{"full":["sleep 30"]}}`, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(bg, 300*time.Millisecond)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, ghURL)
			w.setConfig(tc.cfg)
			before := gitq(t, w.dir, "symbolic-ref", "HEAD") // fails the test when already detached
			ctx, cancel := context.WithCancel(bg)
			if tc.ctx != nil {
				ctx, cancel = tc.ctx()
			}
			defer cancel()
			w.env(false).Gate(ctx, w.dir, GateOpts{Slot: "w1", Base: "main"})
			if after := gitq(t, w.dir, "symbolic-ref", "HEAD"); after != before {
				t.Errorf("HEAD moved from %s to %s", before, after)
			}
		})
	}
}
