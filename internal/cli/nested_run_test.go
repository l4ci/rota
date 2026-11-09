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

// A palette entry starts from the forge, as a typed command would, while the
// cache object stays the outer invocation's. Runs the real paletteVerb entry:
// the verb makes a cached forge read, so a second entry that still saw the
// first one's cache would not reach the forge.
func TestPaletteEntriesStartFromFreshReads(t *testing.T) {
	forgeCalls := 0
	exec := func(context.Context, string, string, []string, []byte) ([]byte, []byte, int, error) {
		forgeCalls++
		return []byte("[]"), nil, 0, nil
	}
	var caches []*tracker.ReadCache
	root := &Command{Name: "rota", Subs: []*Command{{Name: "probe", Verb: func(*flag.FlagSet) RunFunc {
		return func(c *Ctx, _ []string) (Result, error) {
			caches = append(caches, c.deps().ReadCache)
			cli := &tracker.CLI{Provider: "github", Exec: exec, Cache: c.deps().ReadCache,
				LookPath: func(string) (string, error) { return "gh", nil }}
			_, err := cli.Run(c.Context(), []string{"pr", "list"}, nil)
			return Result{Text: "ok"}, err
		}
	}}}}
	deps := testDeps()
	deps.ReadCache = tracker.NewReadCache()
	outer := deps.ReadCache
	c := &Ctx{Deps: deps, Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	e := paletteVerb(c, root, "Probe", "", 0, "probe")
	for i := 0; i < 2; i++ {
		if err := e.Run(); err != nil {
			t.Fatal(err)
		}
	}
	if forgeCalls != 2 {
		t.Fatalf("forge reached %d times for 2 entries: the second saw the first's cached read", forgeCalls)
	}
	if len(caches) != 2 || caches[0] != outer || caches[1] != outer {
		t.Fatalf("palette entries must keep the outer cache object")
	}
}
