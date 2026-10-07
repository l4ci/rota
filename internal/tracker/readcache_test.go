package tracker

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func run(t *testing.T, c *CLI, args ...string) {
	t.Helper()
	if _, err := c.Run(context.Background(), args, nil); err != nil {
		t.Fatal(err)
	}
}

func TestReadCacheMakesIdenticalReadsOnce(t *testing.T) {
	f := &fakeCLI{}
	c := f.cli("github")
	c.Cache = NewReadCache()
	run(t, c, "issue", "list", "--state", "open")
	run(t, c, "issue", "list", "--state", "open")
	run(t, c, "issue", "list", "--state", "closed")
	run(t, c, "api", "repos/o/r/issues/1/comments")
	run(t, c, "api", "repos/o/r/issues/1/comments")
	if got := len(f.calls); got != 3 {
		t.Fatalf("%d gh calls, want 3: %v", got, f.calls)
	}
}

func TestReadCacheDroppedByWrites(t *testing.T) {
	for _, write := range [][]string{
		{"issue", "edit", "1", "--add-label", "x"},
		{"api", "-X", "POST", "repos/o/r/issues/1/comments", "-f", "body=hi"},
		{"pr", "merge", "3"},
	} {
		f := &fakeCLI{}
		c := f.cli("github")
		c.Cache = NewReadCache()
		run(t, c, "issue", "view", "1")
		run(t, c, write...)
		run(t, c, "issue", "view", "1")
		if got := len(f.calls); got != 3 {
			t.Errorf("after %v: %d gh calls, want 3", write, got)
		}
	}
}

func TestReadCacheDoesNotKeepFailures(t *testing.T) {
	f := &fakeCLI{answer: func(n int, _ []string) (string, string, int) {
		if n == 1 {
			return "", "HTTP 500", 1
		}
		return "[]", "", 0
	}}
	c := f.cli("github")
	c.Cache = NewReadCache()
	run(t, c, "issue", "view", "1")
	run(t, c, "issue", "view", "1")
	if len(f.calls) != 2 {
		t.Fatalf("a failed read was cached: %v", f.calls)
	}
}

func TestReadCacheScopesByDirAndProvider(t *testing.T) {
	f := &fakeCLI{}
	rc := NewReadCache()
	a, b := f.cli("github"), f.cli("github")
	a.Dir, b.Dir = "/a", "/b"
	a.Cache, b.Cache = rc, rc
	run(t, a, "issue", "list")
	run(t, b, "issue", "list")
	if len(f.calls) != 2 {
		t.Fatalf("two repos shared an answer: %v", f.calls)
	}
}

func TestGetTakesAListedIssueFromTheList(t *testing.T) {
	f := &fakeCLI{answer: func(_ int, args []string) (string, string, int) {
		if args[1] == "list" {
			return `[{"number":7,"title":"t","body":"b","labels":[{"name":"x"}],"state":"OPEN"}]`, "", 0
		}
		return `{"number":9,"title":"viewed","state":"OPEN"}`, "", 0
	}}
	c := f.cli("github")
	rc := NewReadCache()
	c.Cache = rc
	g := &GitHub{base: base{cli: c, closing: closingGH}}
	ctx := context.Background()
	if _, err := g.List(ctx, ListFilter{}); err != nil {
		t.Fatal(err)
	}
	is, err := g.Get(ctx, 7, false)
	if err != nil || is.Body != "b" {
		t.Fatalf("Get(7) = %+v, %v", is, err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("Get re-fetched a listed issue: %v", f.calls)
	}
	// Unlisted issues and comment reads still go to the forge.
	if _, err := g.Get(ctx, 9, false); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Get(ctx, 7, true); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 {
		t.Fatalf("calls %d, want 3: %v", len(f.calls), f.calls)
	}
	// A write drops the listed copy.
	if err := g.Edit(ctx, 7, IssueEdit{AddLabels: []string{"y"}}); err != nil {
		t.Fatal(err)
	}
	f.calls = nil
	if _, err := g.Get(ctx, 7, false); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 || !strings.Contains(f.last(), "issue view 7") {
		t.Fatalf("stale listed issue served after a write: %v", f.calls)
	}
}

func TestLabelFilteredListComesFromTheCachedOpenList(t *testing.T) {
	f := &fakeCLI{answer: func(_ int, args []string) (string, string, int) {
		return `[{"number":1,"title":"a","labels":[{"name":"in-progress"},{"name":"bug"}],"state":"OPEN"},` +
			`{"number":2,"title":"b","labels":[{"name":"needs-human"}],"state":"OPEN"},` +
			`{"number":3,"title":"c","labels":[],"state":"OPEN"}]`, "", 0
	}}
	c := f.cli("github")
	c.Cache = NewReadCache()
	g := &GitHub{base: base{cli: c, closing: closingGH}}
	ctx := context.Background()
	if _, err := g.List(ctx, ListFilter{}); err != nil {
		t.Fatal(err)
	}
	nums := func(f ListFilter) string {
		t.Helper()
		got, err := g.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var s []string
		for _, is := range got {
			s = append(s, strconv.Itoa(is.Number))
		}
		return strings.Join(s, ",")
	}
	if got := nums(ListFilter{Labels: []string{"In-Progress"}}); got != "1" {
		t.Errorf("in-progress = %q", got)
	}
	if got := nums(ListFilter{State: "open", Labels: []string{"needs-human"}}); got != "2" {
		t.Errorf("needs-human = %q", got)
	}
	if got := nums(ListFilter{Labels: []string{"in-progress", "needs-human"}}); got != "" {
		t.Errorf("both labels = %q", got)
	}
	if got := nums(ListFilter{Labels: []string{"bug"}, Limit: 1}); got != "1" {
		t.Errorf("limit = %q", got)
	}
	if len(f.calls) != 1 {
		t.Fatalf("filtered open lists went to the forge: %v", f.calls)
	}
	// Closed lists, assignee and milestone filters are not answerable from it.
	for _, flt := range []ListFilter{{State: "closed", Labels: []string{"in-progress"}}, {Mine: true, Labels: []string{"bug"}}, {Milestone: "m", Labels: []string{"bug"}}} {
		before := len(f.calls)
		if _, err := g.List(ctx, flt); err != nil {
			t.Fatal(err)
		}
		if len(f.calls) != before+1 {
			t.Errorf("%+v did not reach the forge", flt)
		}
	}
	// A write drops the held list.
	if err := g.Edit(ctx, 1, IssueEdit{AddLabels: []string{"y"}}); err != nil {
		t.Fatal(err)
	}
	before := len(f.calls)
	if _, err := g.List(ctx, ListFilter{Labels: []string{"bug"}}); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != before+1 {
		t.Errorf("stale open list served after a write: %v", f.calls)
	}
}
