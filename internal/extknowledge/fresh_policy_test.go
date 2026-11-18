package extknowledge

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

func TestRefreshPoliciesKeepCloneOnFailure(t *testing.T) {
	for _, mode := range []string{"read", "write"} {
		for _, problem := range []string{"dirty", "diverged", "offline"} {
			t.Run(mode+"/"+problem, func(t *testing.T) {
				f := newFixture(t, "")
				c := f.clone()
				f.remoteAdds("git")
				var sentinel error
				var warning string
				switch problem {
				case "dirty":
					gittest.Write(t, f.a, "topics/go.md", seedGo+"local edit\n")
					sentinel = ErrDirty
					warning = "clone has uncommitted changes and the remote is ahead; not fast-forwarding, reading as-is"
				case "diverged":
					gittest.Commit(t, f.a, "local", "local.md", "keep me\n")
					sentinel = ErrDiverged
					warning = "clone has diverged from its remote; not touching it, reading as-is"
				case "offline":
					gittest.Run(t, f.a, "remote", "set-url", "origin", filepath.Join(f.remote, "gone"))
					sentinel = ErrOffline
					warning = "reading stale copy: git fetch:"
				}
				head := gittest.Run(t, f.a, "rev-parse", "HEAD")
				before := goFile(t, f)
				if mode == "read" {
					got := c.Freshen(context.Background())
					if (problem != "offline" && got != warning) || !strings.HasPrefix(got, warning) || strings.ContainsAny(got, "\r\n") {
						t.Fatalf("warning = %q, want %q", got, warning)
					}
				} else {
					err := c.withCheckout(context.Background(), func() error { return c.syncForWrite(context.Background()) })
					if !errors.Is(err, sentinel) {
						t.Fatalf("syncForWrite = %v, want %v", err, sentinel)
					}
				}
				if gittest.Run(t, f.a, "rev-parse", "HEAD") != head || goFile(t, f) != before {
					t.Fatal("failed refresh changed HEAD or local content")
				}
			})
		}
	}
}

func TestSyncForWriteFastForwardsDespiteFreshStamp(t *testing.T) {
	f := newFixture(t, "")
	c := f.clone()
	c.stamp()
	f.remoteAdds("git")
	if err := c.withCheckout(context.Background(), func() error { return c.syncForWrite(context.Background()) }); err != nil {
		t.Fatal(err)
	}
	if !has(topicNames(t, c), "git") {
		t.Fatal("write must fetch and fast-forward even within the read TTL")
	}
}

func TestRefreshDirtyPreflightDependsOnPolicy(t *testing.T) {
	for _, mode := range []string{"read", "write"} {
		for _, remote := range []string{"absent", "current", "ahead", "offline"} {
			t.Run(mode+"/"+remote, func(t *testing.T) {
				f := newFixture(t, "")
				c := f.clone()
				switch remote {
				case "absent":
					gittest.Run(t, f.a, "remote", "remove", "origin")
				case "ahead":
					gittest.Commit(t, f.a, "local", "local.md", "keep me\n")
				case "offline":
					gittest.Run(t, f.a, "remote", "set-url", "origin", filepath.Join(f.remote, "gone"))
				}
				gittest.Write(t, f.a, "topics/go.md", seedGo+"local edit\n")
				if mode == "write" {
					err := c.withCheckout(context.Background(), func() error { return c.syncForWrite(context.Background()) })
					if !errors.Is(err, ErrDirty) || f.fetchCalls() != 0 {
						t.Fatalf("write must refuse dirty clone before fetching: err=%v, fetches=%d", err, f.fetchCalls())
					}
					return
				}
				warning := c.Freshen(context.Background())
				if remote == "offline" {
					if !strings.HasPrefix(warning, "reading stale copy: ") {
						t.Fatalf("warning = %q, want offline warning", warning)
					}
				} else if warning != "" {
					t.Fatalf("no fast-forward needed: warning = %q", warning)
				}
			})
		}
	}
}
