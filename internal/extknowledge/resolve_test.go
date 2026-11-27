package extknowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
)

const (
	shadowFlat = "# Go Tests\n\n- **Flat** — In the flat file. (seen on go 1.22) <!-- 2026-01-01 source:app -->\n"
	shadowSub  = "# Go: Tests\n\n- **Sub** — In the subtopic. <!-- 2026-01-01 source:app -->\n"
)

// shadowFixture lists the flat "Go Tests" before the split "Go" whose subtopic
// is "Go: Tests": both have the slug go-tests.
func shadowFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, "")
	gittest.Run(t, f.a, "rm", "-q", "topics/go.md")
	gittest.Write(t, f.a, IndexFile, "# Knowledge index\n\n- [Go Tests](topics/go-tests.md) — flat\n- [Go](topics/go/README.md) — language\n")
	gittest.Write(t, f.a, "topics/go-tests.md", shadowFlat)
	gittest.Write(t, f.a, "topics/go/README.md", "# Go\n\n- [Tests](tests.md) — testing\n")
	gittest.Write(t, f.a, "topics/go/tests.md", shadowSub)
	gittest.Run(t, f.a, "add", ".")
	gittest.Run(t, f.a, "commit", "-q", "-m", "shadowed names")
	gittest.Run(t, f.a, "push", "-q")
	return f
}

func TestWriteReachesTheTopicAQueryOfThatNamePrints(t *testing.T) {
	f := shadowFixture(t)
	c := f.clone()
	res, err := c.QueryWith([]string{"Go: Tests"}, QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "**Sub**") || strings.Contains(res.Text, "**Flat**") {
		t.Fatalf("query = %q", res.Text)
	}
	if _, err := c.Add(context.Background(), AddIn{Topic: "Go: Tests", Title: "Added", Body: "new.", Date: "2026-10-25"}); err != nil {
		t.Fatal(err)
	}
	if got := readIn(t, f, "topics/go/tests.md"); !strings.Contains(got, "**Added**") {
		t.Errorf("subtopic = %q", got)
	}
	if got := readIn(t, f, "topics/go-tests.md"); got != shadowFlat {
		t.Errorf("flat file changed: %q", got)
	}
}

func TestTopicWalksReadEachTopicsOwnFile(t *testing.T) {
	f := shadowFixture(t)
	c := f.clone()
	stats, err := c.Stats()
	if err != nil {
		t.Fatal(err)
	}
	sizes := map[string]int{}
	for _, s := range stats {
		sizes[s.File] = s.Bytes
	}
	if sizes["topics/go/tests.md"] != len(shadowSub) || sizes["topics/go-tests.md"] != len(shadowFlat) {
		t.Errorf("stats = %+v", stats)
	}
	if _, err := c.MigrateSeen(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := readIn(t, f, "topics/go/tests.md"); got != shadowSub {
		t.Errorf("subtopic overwritten: %q", got)
	}
	if got := readIn(t, f, "topics/go-tests.md"); !strings.Contains(got, "seen:go@1.22") {
		t.Errorf("flat not migrated: %q", got)
	}
}

// A new subtopic goes under the topic of that exact name, even when an earlier
// topic's file slug matches it too.
func TestNewSubtopicPrefersTheExactParentName(t *testing.T) {
	f := newFixture(t, "")
	gittest.Write(t, f.a, IndexFile, "# Knowledge index\n\n- [Engine](topics/godot.md) — flat\n- [Godot](topics/gd/README.md) — split\n")
	gittest.Write(t, f.a, "topics/godot.md", "# Engine\n")
	gittest.Write(t, f.a, "topics/gd/README.md", "# Godot\n")
	gittest.Run(t, f.a, "add", ".")
	gittest.Run(t, f.a, "commit", "-q", "-m", "shadowed parent")
	gittest.Run(t, f.a, "push", "-q")
	if _, err := f.clone().Add(context.Background(), AddIn{Topic: "Godot: Signals", Title: "Free", Body: "disconnect.", Date: "2026-10-25"}); err != nil {
		t.Fatal(err)
	}
	if got := readIn(t, f, "topics/gd/signals.md"); !strings.Contains(got, "**Free**") {
		t.Fatalf("signals = %q", got)
	}
}

// A title that differs only by surrounding spaces names the same bullet, and a
// new one is written trimmed.
func TestAddTrimsTheTitle(t *testing.T) {
	f := newFixture(t, "")
	add := func(title string) WriteResult {
		t.Helper()
		res, err := f.clone().Add(context.Background(), AddIn{Topic: "Go", Title: title, Body: "b.", Date: "2026-10-25"})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	add("  Padded ")
	if got := readIn(t, f, "topics/go.md"); !strings.Contains(got, "- **Padded** — b.") {
		t.Fatalf("topic = %q, want the trimmed title", got)
	}
	before := readIn(t, f, "topics/go.md")
	add(" padded  ")
	if got := readIn(t, f, "topics/go.md"); got != before {
		t.Fatalf("repeat wrote a second bullet: %q", got)
	}
}
