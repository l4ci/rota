package tracker

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker/itsaplantest"
)

func fakeItsaplan(t *testing.T, o itsaplantest.Options) (*Itsaplan, *itsaplantest.Server) {
	t.Helper()
	s := itsaplantest.New(t, o)
	return itsaplanAt(s, s.Key(), "MKT"), s
}

func itsaplanAt(s *itsaplantest.Server, key, project string) *Itsaplan {
	t := NewItsaplan(connections.Connection{Name: "plan", Kind: connections.KindItsaplan, Site: s.URL}, key, project)
	t.rest.Sleep = func(context.Context, time.Duration) error { return nil }
	return t
}

// AC-1 read side: the board has no limit, so a project past the 500 issues a
// list route would cut short is read whole, in one request.
func TestItsaplanListBeyond500(t *testing.T) {
	it, s := fakeItsaplan(t, itsaplantest.Options{})
	for i := 0; i < 650; i++ {
		s.Add(itsaplantest.Issue{Title: fmt.Sprintf("issue %d", i)})
	}
	got, err := it.List(context.Background(), ListFilter{})
	if err != nil || len(got) != 650 {
		t.Fatalf("%d issues, %v", len(got), err)
	}
	if got[0].Key != "MKT-1" || got[649].Key != "MKT-650" {
		t.Errorf("order: %s ... %s", got[0].Key, got[649].Key)
	}
	if n := s.Count("/projects/MKT/issues/board"); n != 1 {
		t.Errorf("board read %d times, want once per command", n)
	}
	if n := s.Count("/projects/MKT"); n != 1 {
		t.Errorf("project read %d times, want once", n)
	}
}

func TestItsaplanGetNormalizes(t *testing.T) {
	it, s := fakeItsaplan(t, itsaplantest.Options{Labels: []string{"bug"}})
	k := s.Add(itsaplantest.Issue{Title: "Widen", Description: "<p>body</p>", Labels: []string{"bug", "in-progress"}, Fields: "<!-- rota:fields\nA: b\n-->", AssigneeMe: true})
	is, err := it.Get(context.Background(), IssueKey(k), false)
	if err != nil {
		t.Fatal(err)
	}
	if is.Key != "MKT-1" || is.Title != "Widen" || is.State != "open" || !slices.Equal(is.Labels, []string{"bug", "in-progress"}) || !slices.Equal(is.Assignees, []string{"Rota"}) {
		t.Errorf("%+v", is)
	}
	if want := "<p>body</p>\n\n<!-- rota:fields\nA: b\n-->"; is.Body != want {
		t.Errorf("body %q, want the description then the rota field's block %q", is.Body, want)
	}
	for _, bad := range []IssueKey{"OTHER-1", "7", "MKT-99", "nope"} {
		if _, err := it.Get(context.Background(), bad, false); !IsKind(err, KindNotFound) {
			t.Errorf("Get(%q): %v, want not found", bad, err)
		}
	}
}

// A column renamed or added by a project admin must not change which one counts
// as closed: only stateType does.
func TestItsaplanStateByColumnType(t *testing.T) {
	it, s := fakeItsaplan(t, itsaplantest.Options{Columns: []itsaplantest.Column{
		{ID: 7, Name: "Finished", StateType: "completed"}, {ID: 8, Name: "Won't fix", StateType: "canceled"},
		{ID: 9, Name: "Done", StateType: "started"}, {ID: 10, Name: "Queue", StateType: "unstarted"}}})
	s.Add(itsaplantest.Issue{Title: "named Done, still open", Column: 9})
	s.Add(itsaplantest.Issue{Title: "finished", Column: 7})
	s.Add(itsaplantest.Issue{Title: "dropped", Column: 8, Archived: true})
	s.Add(itsaplantest.Issue{Title: "queued", Column: 10})
	titles := func(f ListFilter) []string {
		got, err := it.List(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, is := range got {
			out = append(out, is.Title)
		}
		return out
	}
	if got := titles(ListFilter{}); !slices.Equal(got, []string{"named Done, still open", "queued"}) {
		t.Errorf("open: %v", got)
	}
	if got := titles(ListFilter{State: "closed"}); !slices.Equal(got, []string{"finished", "dropped"}) {
		t.Errorf("closed: %v", got)
	}
	if n := len(titles(ListFilter{State: "all"})); n != 4 {
		t.Errorf("all: %d", n)
	}
	d, _ := it.Get(context.Background(), "MKT-3", false)
	if d.State != "closed" || d.StateReason != CloseNotPlanned {
		t.Errorf("canceled column: %+v", d)
	}
	if d, _ = it.Get(context.Background(), "MKT-2", false); d.State != "closed" || d.StateReason != CloseCompleted {
		t.Errorf("completed column: %+v", d)
	}
}

func TestItsaplanListFilters(t *testing.T) {
	it, s := fakeItsaplan(t, itsaplantest.Options{})
	s.Add(itsaplantest.Issue{Title: "a", Labels: []string{"bug"}, AssigneeMe: true})
	s.Add(itsaplantest.Issue{Title: "b", Labels: []string{"Bug", "p1"}})
	s.Add(itsaplantest.Issue{Title: "c"})
	count := func(f ListFilter) int {
		got, err := it.List(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	if n := count(ListFilter{Labels: []string{"bug"}}); n != 2 {
		t.Errorf("label filter ignores case: %d", n)
	}
	if n := count(ListFilter{Labels: []string{"bug", "p1"}}); n != 1 {
		t.Errorf("labels are all required: %d", n)
	}
	if n := count(ListFilter{Mine: true}); n != 1 {
		t.Errorf("mine: %d", n)
	}
	if n := count(ListFilter{Limit: 2}); n != 2 {
		t.Errorf("limit: %d", n)
	}
	if _, err := it.List(context.Background(), ListFilter{Milestone: "m1"}); err == nil {
		t.Error("a milestone filter was accepted: It's a Plan has none")
	}
}

// AC-3: every way the connection or the project can be wrong is a
// KindUnavailable failure that says what to fix.
func TestItsaplanCheckAuth(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    itsaplantest.Options
		key     string
		project string
		want    string // "" is a pass
	}{
		{name: "ok"},
		{name: "wrong key", key: "itp_wrong", want: "did not accept the API key"},
		{name: "unknown project", project: "NOPE", want: "project NOPE is not visible"},
		{name: "field missing, may create", opts: itsaplantest.Options{NoField: true}},
		{name: "field missing, may not create", opts: itsaplantest.Options{NoField: true, NoFieldRight: true}, want: `no custom field "rota"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := itsaplantest.New(t, tc.opts)
			it := itsaplanAt(s, orDefault(tc.key, s.Key()), orDefault(tc.project, "MKT"))
			err := it.CheckAuth(context.Background())
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("failed: %v", err)
			case tc.want != "" && !IsKind(err, KindUnavailable):
				t.Errorf("%v, want KindUnavailable", err)
			case tc.want != "" && !strings.Contains(err.Error(), tc.want):
				t.Errorf("%q does not say %q", err, tc.want)
			}
		})
	}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func TestItsaplanTesterRejectsKeyAndUnreachable(t *testing.T) {
	s := itsaplantest.New(t, itsaplantest.Options{})
	conn := connections.Connection{Name: "plan", Kind: connections.KindItsaplan, Site: s.URL}
	if err := (ItsaplanTester{}).Test(context.Background(), conn, s.Key()); err != nil {
		t.Fatalf("valid key: %v", err)
	}
	if err := (ItsaplanTester{}).Test(context.Background(), conn, "itp_wrong"); !IsKind(err, KindUnavailable) {
		t.Errorf("rejected key (the instance answers 200 authenticated:false): %v", err)
	}
	s.Close()
	if err := (ItsaplanTester{}).Test(context.Background(), conn, s.Key()); !IsKind(err, KindUnavailable) {
		t.Errorf("unreachable url: %v", err)
	}
	conn.Site = ""
	if err := (ItsaplanTester{}).Test(context.Background(), conn, "k"); err == nil {
		t.Error("a connection with no site was accepted")
	}
}

// issues.source itsaplan builds the tracker from issues.connection and
// issues.itsaplan.project, and says which one is missing.
func TestNewIssueTrackerItsaplan(t *testing.T) {
	s := itsaplantest.New(t, itsaplantest.Options{})
	conn := WithConnections(func(_ context.Context, name string) (connections.Connection, string, error) {
		return connections.Connection{Name: name, Kind: connections.KindItsaplan, Site: s.URL}, s.Key(), nil
	})
	build := func(cfg string, opts ...Option) (IssueTracker, error) {
		c, err := jsonx.Decode([]byte(cfg))
		if err != nil {
			t.Fatal(err)
		}
		return NewIssueTrackerFromConfig(context.Background(), c, "", opts...)
	}
	it, err := build(`{"issues":{"source":"itsaplan","connection":"plan","itsaplan":{"project":"mkt"}}}`, conn)
	if err != nil {
		t.Fatal(err)
	}
	if it.Provider() != "itsaplan" || it.CheckAuth(context.Background()) != nil {
		t.Errorf("%s: %v", it.Provider(), it.CheckAuth(context.Background()))
	}
	for cfg, want := range map[string]string{
		`{"issues":{"source":"itsaplan","itsaplan":{"project":"MKT"}}}`:    "issues.connection is not set",
		`{"issues":{"source":"itsaplan","connection":"plan"}}`:             "issues.itsaplan.project is not set",
		`{"issues":{"source":"itsaplan","connection":"plan","linear":{}}}`: "issues.itsaplan.project is not set",
	} {
		if _, err := build(cfg, conn); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", cfg, err, want)
		}
	}
	wrongKind := WithConnections(func(_ context.Context, name string) (connections.Connection, string, error) {
		return connections.Connection{Name: name, Kind: connections.KindLinear}, "k", nil
	})
	if _, err := build(`{"issues":{"source":"itsaplan","connection":"plan","itsaplan":{"project":"MKT"}}}`, wrongKind); err == nil || !strings.Contains(err.Error(), "not itsaplan") {
		t.Errorf("wrong connection kind: %v", err)
	}
	if got, err := config.IssueSource(mustJSON(t, `{"issues":{"source":"itsaplan"}}`)); err != nil || got != config.SourceItsaplan {
		t.Errorf("issues.source itsaplan: %q %v", got, err)
	}
}

func mustJSON(t *testing.T, s string) any {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestItsaplanClosesIssueByKey(t *testing.T) {
	it := NewItsaplan(connections.Connection{Site: "http://x"}, "k", "MKT")
	if !it.ClosesIssue("MKT-7", ClosingSource{Branch: "ben/MKT-7-widen"}) || !it.ClosesIssue("MKT-7", ClosingSource{Title: "Fix MKT-7"}) {
		t.Error("the key in the branch or the title closes the issue")
	}
	if it.ClosesIssue("MKT-7", ClosingSource{Branch: "ben/MKT-77-widen", Body: "Closes MKT-7"}) {
		t.Error("a longer key, or the body, closed it")
	}
}

// The comment feed is cursor-paged: every page is read, oldest comment first,
// and Get with comments carries them.
func TestItsaplanCommentsSpanPages(t *testing.T) {
	it, s := fakeItsaplan(t, itsaplantest.Options{FeedPage: 2})
	k := s.Add(itsaplantest.Issue{Title: "talk", Comments: []string{"one", "Claimed by ben@1", "three", "four", "five"}})
	cs, err := it.Comments(context.Background(), IssueKey(k))
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, c := range cs {
		bodies = append(bodies, c.Body)
	}
	if !slices.Equal(bodies, []string{"one", "Claimed by ben@1", "three", "four", "five"}) {
		t.Errorf("comments: %v", bodies)
	}
	if cs[0].Author != "Rota" || cs[0].CreatedAt.IsZero() || cs[0].ID == cs[1].ID {
		t.Errorf("comment fields: %+v", cs[:2])
	}
	if got, want := len(feedRequests(s)), 3; got != want {
		t.Errorf("%d feed pages read, want %d", got, want)
	}
	is, err := it.Get(context.Background(), IssueKey(k), true)
	if err != nil || len(is.Comments) != 5 {
		t.Errorf("Get with comments: %d, %v", len(is.Comments), err)
	}
}

func feedRequests(s *itsaplantest.Server) []string {
	var out []string
	for _, r := range s.Requests() {
		if strings.HasSuffix(r, "/feed") {
			out = append(out, r)
		}
	}
	return out
}
