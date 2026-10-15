package tracker

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/connections"
	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker/lineartest"
)

const (
	linearKey    = "lin_api_test"
	fieldsURL    = "https://rota.invalid/state/fields"
	commentURLPf = "https://rota.invalid/state/comment/"
)

// fakeLinear is a Linear adapter for the fake's team, with no real sleeps.
func fakeLinear(t *testing.T, o lineartest.Options) (*Linear, *lineartest.Server) {
	t.Helper()
	s := lineartest.New(t, o)
	return linearAt(s, linearKey, "ENG"), s
}

func linearAt(s *lineartest.Server, key, team string) *Linear {
	l := NewLinear(connections.Connection{Name: "work", Kind: connections.KindLinear, Site: s.URL}, key, team)
	l.rest.Sleep = func(context.Context, time.Duration) error { return nil }
	return l
}

func linearCreate(t *testing.T, l *Linear, title, body string, labels ...string) IssueKey {
	t.Helper()
	k, err := l.Create(context.Background(), title, body, labels, "")
	if err != nil {
		t.Fatalf("Create %q: %v", title, err)
	}
	return k
}

// shape asserts the latest document sent for op names each part (whitespace
// collapsed), since the fake does not parse selection sets, and returns the
// variables it was sent with.
func shape(t *testing.T, s *lineartest.Server, op string, parts ...string) map[string]any {
	t.Helper()
	doc, vars := s.Last(op)
	if doc == "" {
		t.Fatalf("%s was never sent", op)
	}
	for _, p := range parts {
		if !strings.Contains(doc, p) {
			t.Errorf("%s does not send %q:\n%s", op, p, doc)
		}
	}
	return vars
}

// input is the input variable of a mutation.
func input(vars map[string]any) map[string]any {
	in, _ := vars["input"].(map[string]any)
	return in
}

// since is the operations the fake saw after the first n requests.
func since(s *lineartest.Server, n int) []string { return s.Requests()[n:] }

func TestLinearCreateGetEdit(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	fields := marker.FieldsBlock([]string{"Priority: P1"})
	k := linearCreate(t, l, "title", "text\n\n"+fields, "bug", "bug", "p1")
	if k != "ENG-1" {
		t.Fatalf("key = %s", k)
	}
	is, err := l.Get(ctx, k, false)
	if err != nil {
		t.Fatal(err)
	}
	if is.Title != "title" || is.State != "open" || is.Body != "text\n\n"+fields || !slices.Equal(is.Labels, []string{"bug", "p1"}) {
		t.Fatalf("Get = %+v", is)
	}
	if is.Author != "Me" || !strings.HasSuffix(is.URL, "/ENG-1") || len(is.Assignees) != 0 {
		t.Fatalf("Get = %+v", is)
	}
	if d := s.Description(string(k)); strings.TrimSpace(d) != "text" {
		t.Fatalf("description = %q, want the text only", d)
	}
	if got := s.Attachments(string(k)); len(got) != 1 || got[fieldsURL] != fields {
		t.Fatalf("attachments = %v, want the fields block", got)
	}

	nt, nb := "renamed", "new body"
	if err := l.Edit(ctx, k, IssueEdit{Title: &nt, Body: &nb, AddLabels: []string{"x"}, RemoveLabels: []string{"bug", "never-made"}}); err != nil {
		t.Fatal(err)
	}
	is, _ = l.Get(ctx, k, false)
	if is.Title != "renamed" || is.Body != "new body" || !slices.Equal(is.Labels, []string{"p1", "x"}) {
		t.Fatalf("after edit = %+v", is)
	}
	if got := s.Attachments(string(k)); len(got) != 0 {
		t.Fatalf("a body without a fields block left attachments %v", got)
	}
	n := len(s.Requests())
	if err := l.Edit(ctx, k, IssueEdit{}); err != nil || slices.Contains(since(s, n), "IssueUpdate") {
		t.Fatalf("empty edit = %v, requests %v", err, since(s, n))
	}
}

func TestLinearLabels(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	if err := l.EnsureLabels(ctx, []string{"missing"}, false); !IsKind(err, KindNotFound) {
		t.Fatalf("EnsureLabels without autoCreate = %v, want not found", err)
	}
	s.AddLabel("ws", "")
	s.AddLabel("ops-only", "OPS")
	n := len(s.Requests())
	k := linearCreate(t, l, "t", "", "WS", "ops-only")
	if got := s.Labels(string(k)); !slices.Equal(got, []string{"ws", "ops-only"}) {
		t.Fatalf("labels = %v", got)
	}
	// the workspace label is reused; another team's is never used, so ENG gets its own
	if creates := strings.Count(strings.Join(since(s, n), " "), "LabelCreate"); creates != 1 {
		t.Fatalf("LabelCreate ran %d times, want 1 (ops-only)", creates)
	}
	if err := l.EnsureLabels(ctx, []string{"ops-only", "ws"}, false); err != nil {
		t.Fatalf("the created label should exist for ENG: %v", err)
	}
	if err := l.AddLabels(ctx, k, []string{"nope"}, false); !IsKind(err, KindNotFound) {
		t.Fatalf("AddLabels without autoCreate = %v", err)
	}
	if err := l.AddLabels(ctx, k, []string{"nope"}, true); err != nil {
		t.Fatal(err)
	}
	if err := l.RemoveLabels(ctx, k, []string{"ws"}); err != nil {
		t.Fatal(err)
	}
	if got := s.Labels(string(k)); !slices.Equal(got, []string{"ops-only", "nope"}) {
		t.Fatalf("labels = %v", got)
	}
}

func TestLinearMarkerComments(t *testing.T) {
	ctx := context.Background()
	// a page of two forces the state attachments onto a second page
	l, s := fakeLinear(t, lineartest.Options{PageSize: 2})
	k := linearCreate(t, l, "t", "text\n\n"+marker.FieldsBlock([]string{"Priority: P1"}))
	proof := "<!-- rota:proof -->\n## Proof\n- row"
	feedback := "<!-- rota:comment feedback -->\nhello"
	pid, err := l.AddComment(ctx, k, proof)
	if err != nil {
		t.Fatal(err)
	}
	fid, err := l.AddComment(ctx, k, feedback)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := l.AddComment(ctx, k, "just words")
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range s.CommentBodies(string(k)) {
		if strings.Contains(raw, "<!--") || strings.Contains(raw, "rota:") {
			t.Errorf("marker leaked into a visible comment: %q", raw)
		}
	}
	if got := s.Attachments(string(k)); len(got) != 3 || got[commentURLPf+plain] != "" || got[commentURLPf+pid] != proof {
		t.Fatalf("attachments = %v: want fields, proof and feedback but nothing for the plain comment", got)
	}
	n := len(s.Requests())
	cs, err := l.Comments(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 3 || cs[0].Body != proof || cs[1].Body != feedback || cs[2].Body != "just words" || cs[0].Author != "Me" || cs[0].CreatedAt.IsZero() {
		t.Fatalf("Comments = %+v", cs)
	}
	if !slices.Contains(since(s, n), "Attachments") {
		t.Fatalf("a second attachment page was not read: %v", since(s, n))
	}
	if is, _ := l.Get(ctx, k, true); len(is.Comments) != 3 || is.Comments[0].Body != proof {
		t.Fatalf("Get with comments = %+v", is.Comments)
	}
	shape(t, s, "Issue", "query Issue($id: String!)", "issue(id: $id) {", "identifier title description url",
		"state { type }", "attachments(first: 50) { nodes { id url metadata } pageInfo { hasNextPage endCursor } }")
	shape(t, s, "Comments", "query Comments($id: String!, $after: String)",
		"comments(first: 50, after: $after) { nodes { id body createdAt user { id name displayName } } pageInfo { hasNextPage endCursor } }")
	shape(t, s, "Attachments", "attachments(first: 50, after: $after) { nodes { id url metadata }")

	if err := l.EditComment(ctx, k, pid, "<!-- rota:proof -->\nchanged"); err != nil {
		t.Fatal(err)
	}
	if got := s.Attachments(string(k))[commentURLPf+pid]; got != "<!-- rota:proof -->\nchanged" {
		t.Fatalf("edited attachment = %q", got)
	}
	// the upsert names the comment's URL again, so Linear replaces it
	in := input(shape(t, s, "AttachmentCreate", "mutation AttachmentCreate($input: AttachmentCreateInput!)", "attachmentCreate(input: $input) { success }"))
	if md, _ := in["metadata"].(map[string]any); in["url"] != commentURLPf+pid || md["body"] != "<!-- rota:proof -->\nchanged" || in["issueId"] == "" {
		t.Errorf("attachment upsert input = %v", in)
	}
	if err := l.EditComment(ctx, k, pid, "no markers now"); err != nil {
		t.Fatal(err)
	}
	if _, kept := s.Attachments(string(k))[commentURLPf+pid]; kept {
		t.Fatal("editing to a body without markers kept the attachment")
	}
	if err := l.DeleteComment(ctx, k, fid); err != nil {
		t.Fatal(err)
	}
	if _, kept := s.Attachments(string(k))[commentURLPf+fid]; kept || len(s.CommentBodies(string(k))) != 2 {
		t.Fatalf("delete left attachment or comment: %v %v", s.Attachments(string(k)), s.CommentBodies(string(k)))
	}
	if u, err := l.CommentURL(ctx, k, plain); err != nil || !strings.Contains(u, "/ENG-1#comment-") {
		t.Fatalf("CommentURL = %q, %v", u, err)
	}
	if _, err := l.CommentURL(ctx, k, "nope"); !IsKind(err, KindNotFound) {
		t.Fatalf("CommentURL of a missing comment = %v", err)
	}
}

func TestLinearList(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{PageSize: 2})
	var keys []IssueKey
	for i := 1; i <= 11; i++ {
		var labels []string
		if i == 3 || i == 10 {
			labels = []string{"a"}
		}
		if i == 10 {
			labels = append(labels, "b")
		}
		keys = append(keys, linearCreate(t, l, "issue", "", labels...))
	}
	if err := l.Close(ctx, keys[1], "completed", ""); err != nil { // ENG-2
		t.Fatal(err)
	}
	if err := l.AssignSelf(ctx, keys[3]); err != nil { // ENG-4
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		f    ListFilter
		want []IssueKey
	}{
		{"open", ListFilter{}, []IssueKey{"ENG-1", "ENG-3", "ENG-4", "ENG-5", "ENG-6", "ENG-7", "ENG-8", "ENG-9", "ENG-10", "ENG-11"}},
		{"closed", ListFilter{State: "closed"}, []IssueKey{"ENG-2"}},
		{"all sorted by number", ListFilter{State: "all"}, append(slices.Clone(keys[:9]), "ENG-10", "ENG-11")},
		{"labels are all-of", ListFilter{State: "all", Labels: []string{"A", "b"}}, []IssueKey{"ENG-10"}},
		{"one label", ListFilter{Labels: []string{"a"}}, []IssueKey{"ENG-3", "ENG-10"}},
		{"mine", ListFilter{State: "all", Mine: true}, []IssueKey{"ENG-4"}},
		{"limit", ListFilter{Limit: 3}, []IssueKey{"ENG-1", "ENG-3", "ENG-4"}},
	} {
		got, err := l.List(ctx, c.f)
		var ks []IssueKey
		for _, i := range got {
			ks = append(ks, i.Key)
		}
		if err != nil || !slices.Equal(ks, c.want) {
			t.Errorf("%s: List = %v, %v; want %v", c.name, ks, err, c.want)
		}
	}
	if _, err := l.List(ctx, ListFilter{Labels: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	vars := shape(t, s, "Issues", "query Issues($filter: IssueFilter, $after: String)", "issues(filter: $filter, first: 50, after: $after) {",
		"identifier", "state { type }", "labels(first: 50) { nodes { name } }", "attachments(first: 50)", "pageInfo { hasNextPage endCursor } } }")
	want := `{"and":[{"labels":{"some":{"name":{"eqIgnoreCase":"a"}}}}],"state":{"type":{"nin":["completed","canceled"]}},"team":{"key":{"eq":"ENG"}}}`
	if got, _ := json.Marshal(vars["filter"]); string(got) != want {
		t.Errorf("filter = %s\nwant     %s", got, want)
	}
	n := len(s.Requests())
	if _, err := l.List(ctx, ListFilter{State: "all"}); err != nil || strings.Count(strings.Join(since(s, n), " "), "Issues") < 6 {
		t.Fatalf("11 issues at 2 a page took requests %v", since(s, n))
	}
	if _, err := l.List(ctx, ListFilter{Milestone: "M01"}); err == nil {
		t.Fatal("List with a milestone succeeded")
	}
}

func TestLinearStatus(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	k := linearCreate(t, l, "t", "")
	if s.StateType(string(k)) != "unstarted" {
		t.Fatalf("new issue is %s", s.StateType(string(k)))
	}
	// the fake's states by position: Todo state-2 (unstarted), Done state-4
	// (completed), Canceled state-5 before Duplicate state-6 (canceled)
	for _, c := range []struct{ reason, state, typ, id string }{{"completed", "completed", "completed", "state-4"}, {"not_planned", "canceled", "not_planned", "state-5"}} {
		if err := l.Close(ctx, k, c.reason, "closing note"); err != nil {
			t.Fatal(err)
		}
		if in := input(shape(t, s, "IssueUpdate", "mutation IssueUpdate($id: String!, $input: IssueUpdateInput!)", "issueUpdate(id: $id, input: $input) { success }")); len(in) != 1 || in["stateId"] != c.id {
			t.Errorf("Close %s sent input %v, want only stateId %s", c.reason, in, c.id)
		}
		is, _ := l.Get(ctx, k, false)
		if s.StateType(string(k)) != c.state || is.State != "closed" || is.StateReason != c.typ || is.ClosedAt == "" {
			t.Fatalf("Close %s: type %s, issue %+v", c.reason, s.StateType(string(k)), is)
		}
		if err := l.Reopen(ctx, k); err != nil {
			t.Fatal(err)
		}
		if in := input(shape(t, s, "IssueUpdate")); in["stateId"] != "state-2" {
			t.Errorf("Reopen sent input %v, want stateId state-2", in)
		}
		if is, _ = l.Get(ctx, k, false); is.State != "open" || is.ClosedAt != "" || s.StateType(string(k)) != "unstarted" {
			t.Fatalf("Reopen: %+v", is)
		}
	}
	shape(t, s, "Team", "query Team($key: String!)", "teams(filter: {key: {eq: $key}}) { nodes { id key states(first: 250) { nodes { id type position } } } }")
	if got := s.CommentBodies(string(k)); len(got) != 2 || got[0] != "closing note" {
		t.Fatalf("closing comments = %v", got)
	}
	n := len(s.Requests())
	if err := l.Reopen(ctx, k); err != nil || slices.Contains(since(s, n), "IssueUpdate") {
		t.Fatalf("Reopen of an open issue = %v, requests %v", err, since(s, n))
	}

	if err := l.CloseAfterMerge(ctx, k, "PR #7"); err != nil {
		t.Fatal(err)
	}
	if s.StateType(string(k)) != "completed" || !slices.Contains(s.CommentBodies(string(k)), "Closed by rota: PR #7 merged.") {
		t.Fatalf("CloseAfterMerge left %s, comments %v", s.StateType(string(k)), s.CommentBodies(string(k)))
	}
	n = len(s.Requests())
	if err := l.CloseAfterMerge(ctx, k, "PR #7"); err != nil {
		t.Fatal(err)
	}
	for _, op := range since(s, n) {
		if op == "IssueUpdate" || op == "CommentCreate" {
			t.Fatalf("CloseAfterMerge on a closed issue wrote: %v", since(s, n))
		}
	}
}

func TestLinearAssignSelf(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	k := linearCreate(t, l, "t", "")
	if err := l.AssignSelf(ctx, k); err != nil {
		t.Fatal(err)
	}
	if is, _ := l.Get(ctx, k, false); !slices.Equal(is.Assignees, []string{"Me"}) || s.Assignee(string(k)) != "Me" {
		t.Fatalf("assignees = %v", is.Assignees)
	}
}

func TestLinearErrors(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	k := linearCreate(t, l, "t", "")

	if err := linearAt(s, "wrong", "ENG").CheckAuth(ctx); !IsKind(err, KindUnavailable) {
		t.Errorf("wrong key = %v, want unavailable", err)
	}
	s.ErrorNext(1, "RATELIMITED", "slow down")
	if _, err := l.Get(ctx, k, false); !IsKind(err, KindRateLimited) {
		t.Errorf("RATELIMITED error = %v", err)
	}
	s.RateLimitNext(1, 400)
	if _, err := l.Get(ctx, k, false); !IsKind(err, KindRateLimited) {
		t.Errorf("HTTP 400 rate limit = %v", err)
	}
	s.RateLimitNext(1, 429)
	if _, err := l.Get(ctx, k, false); err != nil {
		t.Errorf("a 429 is waited out and retried: %v", err)
	}
	for _, key := range []IssueKey{"ENG-999", "42", "nope"} {
		n := len(s.Requests())
		if _, err := l.Get(ctx, key, false); !IsKind(err, KindNotFound) {
			t.Errorf("Get(%q) = %v, want not found", key, err)
		}
		if key == "42" || key == "nope" {
			if got := since(s, n); len(got) != 0 {
				t.Errorf("Get(%q) sent %v", key, got)
			}
		}
	}

	// a read goes through REST.Query: the 502 is retried and the same
	// operation is sent again
	s.FailNext(1, 502)
	n := len(s.Requests())
	if _, err := l.Get(ctx, k, false); err != nil {
		t.Errorf("a query is retried after a 502: %v", err)
	}
	if got := since(s, n); !slices.Equal(got, []string{"Issue", "Issue"}) {
		t.Errorf("a failed read sent %v, want Issue twice", got)
	}
	s.FailNext(1, 502)
	n = len(s.Requests())
	if _, err := l.AddComment(ctx, k, "once"); err == nil {
		t.Error("a mutation was retried or hid its 502")
	}
	if got := since(s, n); !slices.Equal(got, []string{"CommentCreate"}) {
		t.Errorf("a failed mutation sent %v, want CommentCreate once", got)
	}
	if got := s.CommentBodies(string(k)); len(got) != 0 {
		t.Errorf("comments after a failed AddComment: %v", got)
	}
}

func TestLinearNoMilestones(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	k := linearCreate(t, l, "t", "")
	n := len(s.Requests())
	if _, err := l.Create(ctx, "t", "", nil, "M01"); err == nil {
		t.Error("Create with a milestone succeeded")
	}
	ms := "M01"
	if err := l.Edit(ctx, k, IssueEdit{Milestone: ms}); err == nil {
		t.Error("Edit with a milestone succeeded")
	}
	if got := since(s, n); len(got) != 0 {
		t.Errorf("a refused milestone still sent %v", got)
	}
	if _, err := l.Milestones(ctx, "open"); err == nil {
		t.Error("Milestones succeeded")
	}
	if _, err := l.CreateMilestone(ctx, "M01", ""); err == nil {
		t.Error("CreateMilestone succeeded")
	}
	if _, err := l.IssuesInMilestone(ctx, "M01", "open"); err == nil {
		t.Error("IssuesInMilestone succeeded")
	}
	if _, ok, err := l.FindMilestone(ctx, "M01"); ok || err != nil {
		t.Errorf("FindMilestone = %v, %v; want none", ok, err)
	}
}

func TestLinearTeamAndTester(t *testing.T) {
	ctx := context.Background()
	s := lineartest.New(t, lineartest.Options{})
	if err := linearAt(s, linearKey, "NOPE").CheckAuth(ctx); !IsKind(err, KindUnavailable) || !strings.Contains(err.Error(), "issues.linear.team") {
		t.Errorf("unknown team = %v", err)
	}
	if err := linearAt(s, linearKey, "eng").CheckAuth(ctx); err != nil {
		t.Errorf("a lower-case team key: %v", err)
	}
	c := connections.Connection{Name: "work", Kind: connections.KindLinear, Site: s.URL}
	if err := (LinearTester{}).Test(ctx, c, linearKey); err != nil {
		t.Errorf("Tester with the right key: %v", err)
	}
	if err := (LinearTester{}).Test(ctx, c, "wrong"); !IsKind(err, KindUnavailable) {
		t.Errorf("Tester with a wrong key = %v, want unavailable", err)
	}
}

// Linear documents no limit on attachment metadata, so rota caps a state
// body at 64 KiB and fails above it, leaving no half-written comment.
func TestLinearStateOverTheCapFailsLoudly(t *testing.T) {
	ctx := context.Background()
	l, s := fakeLinear(t, lineartest.Options{})
	k := linearCreate(t, l, "t", "")
	big := "<!-- rota:proof -->\n" + strings.Repeat("x", 64<<10)
	if _, err := l.AddComment(ctx, k, big); err == nil || !strings.Contains(err.Error(), "caps") {
		t.Fatalf("oversized state: %v, want a failure naming the cap", err)
	}
	if got := s.CommentBodies(string(k)); len(got) != 0 {
		t.Errorf("comments left behind: %d", len(got))
	}
	if got := s.Attachments(string(k)); len(got) != 0 {
		t.Errorf("attachments left behind: %v", got)
	}
}
