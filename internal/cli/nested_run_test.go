package cli

import (
	"bytes"
	"context"
	"flag"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// A nested run (palette entry, projects screen, browse view) is part of the
// outer invocation: it keeps the outer read cache and the caller's context.
func TestNestedRunKeepsOuterCacheAndContext(t *testing.T) {
	type ctxKey struct{}
	var seen context.Context
	var seenCache *tracker.ReadCache
	root := &Command{Name: "rota", Subs: []*Command{{Name: "probe", Verb: func(*flag.FlagSet) RunFunc {
		return func(c *Ctx, _ []string) (Result, error) {
			seen, seenCache = c.Context(), c.deps().ReadCache
			return Result{Text: "ok"}, nil
		}
	}}}}
	deps := testDeps()
	deps.ReadCache = tracker.NewReadCache()
	outer := deps.ReadCache
	ctx := context.WithValue(context.Background(), ctxKey{}, "caller")
	c := &Ctx{Deps: deps, ctx: ctx}

	var out, errb bytes.Buffer
	if code := c.runNested(root, []string{"probe"}, strings.NewReader(""), &out, &errb, nil); code != ExitOK {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if deps.ReadCache != outer || seenCache != outer {
		t.Fatalf("nested run replaced the outer read cache")
	}
	if seen.Value(ctxKey{}) != "caller" {
		t.Fatalf("nested run lost the caller's context")
	}
}
