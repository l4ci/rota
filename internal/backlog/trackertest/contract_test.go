package trackertest_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/tracker"
)

// issueAPI is the slice of tracker.Adapter that both fakes model: the
// in-memory Fake the Go tests inject, and the stateful test/fakes/gh (Python)
// that the real GitHub adapter and the cmd/rota scenarios talk to.
type issueAPI interface {
	Create(ctx context.Context, title, body string, labels []string, milestone string) (int, error)
	Get(ctx context.Context, n int, withComments bool) (tracker.Issue, error)
	List(ctx context.Context, f tracker.ListFilter) ([]tracker.Issue, error)
	Edit(ctx context.Context, n int, e tracker.IssueEdit) error
	EnsureLabels(ctx context.Context, names []string, autoCreate bool) error
	AddLabels(ctx context.Context, n int, labels []string, autoCreate bool) error
	RemoveLabels(ctx context.Context, n int, labels []string) error
	Close(ctx context.Context, n int, reason, comment string) error
	Reopen(ctx context.Context, n int) error
	Comments(ctx context.Context, n int) ([]tracker.Comment, error)
	AddComment(ctx context.Context, n int, body string) (string, error)
	EditComment(ctx context.Context, n int, id, body string) error
	DeleteComment(ctx context.Context, n int, id string) error
}

// pyFake is the real GitHub adapter driving test/fakes/gh over a fresh store.
func pyFake(t *testing.T) issueAPI {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "test", "fakes"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_TRACKER_DB", filepath.Join(t.TempDir(), "tracker.json"))
	dir := gittest.NewRepo(t, "main")
	a, err := tracker.New(context.Background(), tracker.Settings{Provider: "github"}, "github", dir)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func goFake(*testing.T) issueAPI { return &trackertest.Fake{} }

var fakes = []struct {
	name string
	new  func(*testing.T) issueAPI
}{{"go", goFake}, {"python-gh", pyFake}}

// view is what a caller can observe of an issue, minus what a fake mints
// (URLs, timestamps, comment ids).
type view struct {
	Number      int
	Title, Body string
	Labels      []string
	Milestone   string
	State       string
	StateReason string
	Assignees   []string
	Comments    []string
}

// observe flattens an issue; a nil list and an empty one are the same to a
// caller, and label order is the forge's business.
func observe(i tracker.Issue) view {
	v := view{Number: i.Number, Title: i.Title, Body: i.Body, Labels: append([]string{}, i.Labels...), Milestone: i.Milestone,
		State: i.State, StateReason: i.StateReason, Assignees: append([]string{}, i.Assignees...), Comments: []string{}}
	sort.Strings(v.Labels)
	for _, c := range i.Comments {
		v.Comments = append(v.Comments, c.Body)
	}
	return v
}

// scenario drives an issueAPI and returns a transcript of what it observed.
// The same script runs against every fake; the transcripts must be equal.
type scenario func(t *testing.T, ctx context.Context, a issueAPI) []any

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, ctx context.Context, a issueAPI, n int) view {
	t.Helper()
	return observe(must[tracker.Issue](t)(a.Get(ctx, n, true)))
}

var scenarios = map[string]scenario{
	"create then get": func(t *testing.T, ctx context.Context, a issueAPI) []any {
		ok(t, a.EnsureLabels(ctx, []string{"bug", "p1"}, true))
		n1 := must[int](t)(a.Create(ctx, "first", "body one", []string{"bug", "p1"}, ""))
		n2 := must[int](t)(a.Create(ctx, "second", "", nil, ""))
		return []any{n1, n2, get(t, ctx, a, n1), get(t, ctx, a, n2)}
	},
	"edit title body labels": func(t *testing.T, ctx context.Context, a issueAPI) []any {
		// A forge rejects a label that does not exist, the Fake does not model
		// that: a test must create labels first on either.
		ok(t, a.EnsureLabels(ctx, []string{"a", "b", "c", "d"}, true))
		n := must[int](t)(a.Create(ctx, "t", "b", []string{"a", "b"}, ""))
		title, body := "t2", "b2"
		ok(t, a.Edit(ctx, n, tracker.IssueEdit{Title: &title, Body: &body, AddLabels: []string{"c"}, RemoveLabels: []string{"a"}}))
		ok(t, a.AddLabels(ctx, n, []string{"b", "d"}, true))
		ok(t, a.RemoveLabels(ctx, n, []string{"c", "absent"}))
		return []any{get(t, ctx, a, n)}
	},
	"close reopen": func(t *testing.T, ctx context.Context, a issueAPI) []any {
		n := must[int](t)(a.Create(ctx, "t", "b", nil, ""))
		ok(t, a.Close(ctx, n, "", ""))
		done := get(t, ctx, a, n)
		ok(t, a.Reopen(ctx, n))
		reopened := get(t, ctx, a, n)
		ok(t, a.Close(ctx, n, "not_planned", "no thanks"))
		return []any{done, reopened, get(t, ctx, a, n)}
	},
	"list filters by state in number order": func(t *testing.T, ctx context.Context, a issueAPI) []any {
		var ns []int
		for _, title := range []string{"a", "b", "c"} {
			ns = append(ns, must[int](t)(a.Create(ctx, title, "", nil, "")))
		}
		ok(t, a.Close(ctx, ns[1], "", ""))
		var out []any
		for _, st := range []string{"", "open", "closed", "all"} {
			var titles []string
			for _, i := range must[[]tracker.Issue](t)(a.List(ctx, tracker.ListFilter{State: st})) {
				titles = append(titles, fmt.Sprintf("%d:%s:%s", i.Number, i.Title, i.State))
			}
			out = append(out, titles)
		}
		return out
	},
	"comments add edit delete": func(t *testing.T, ctx context.Context, a issueAPI) []any {
		n := must[int](t)(a.Create(ctx, "t", "b", nil, ""))
		id1 := must[string](t)(a.AddComment(ctx, n, "one"))
		id2 := must[string](t)(a.AddComment(ctx, n, "two"))
		ok(t, a.EditComment(ctx, n, id1, "one!"))
		ok(t, a.DeleteComment(ctx, n, id2))
		var bodies []string
		for _, c := range must[[]tracker.Comment](t)(a.Comments(ctx, n)) {
			bodies = append(bodies, c.Body)
		}
		return []any{bodies, get(t, ctx, a, n)}
	},
	"unknown issue is not found": func(t *testing.T, ctx context.Context, a issueAPI) []any {
		var out []any
		for _, err := range []error{
			func() error { _, e := a.Get(ctx, 99, false); return e }(),
			a.Edit(ctx, 99, tracker.IssueEdit{AddLabels: []string{"x"}}),
			a.Close(ctx, 99, "", ""),
			a.Reopen(ctx, 99),
		} {
			out = append(out, tracker.IsKind(err, tracker.KindNotFound))
		}
		return out
	},
}

func TestFakesAgree(t *testing.T) {
	ctx := context.Background()
	names := make([]string, 0, len(scenarios))
	for n := range scenarios {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			var want []any
			for i, f := range fakes {
				got := scenarios[name](t, ctx, f.new(t))
				if i == 0 {
					want = got
					continue
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s and %s disagree\n%s: %+v\n%s: %+v", fakes[0].name, f.name, fakes[0].name, want, f.name, got)
				}
			}
		})
	}
}
