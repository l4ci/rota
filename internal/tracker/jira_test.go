package tracker

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/marker"
	"github.com/l4ci/rota/internal/tracker/jiratest"
)

var jiraVersions = []int{3, 2}

func eachJira(t *testing.T, o jiratest.Options, f func(t *testing.T, j *Jira, s *jiratest.Server)) {
	t.Helper()
	for _, v := range jiraVersions {
		t.Run(fmt.Sprintf("v%d", v), func(t *testing.T) {
			o := o
			o.Version = v
			j, s := fakeJira(t, o)
			f(t, j, s)
		})
	}
}

func mustCreate(t *testing.T, j *Jira, title, body string, labels []string, ms string) IssueKey {
	t.Helper()
	k, err := j.Create(context.Background(), title, body, labels, ms)
	if err != nil {
		t.Fatalf("Create %q: %v", title, err)
	}
	return k
}

func keysOf(is []Issue) []IssueKey {
	out := []IssueKey{}
	for _, i := range is {
		out = append(out, i.Key)
	}
	return out
}

func human(s *jiratest.Server, key IssueKey, text string) string {
	body := fmt.Sprintf("%q", text)
	if s.Version() == 3 {
		body = fmt.Sprintf(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":%q}]}]}`, text)
	}
	return s.AddHumanComment(string(key), []byte(body), "pat")
}

func TestJiraCreateGetEdit(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		ms, err := j.CreateMilestone(ctx, "M01 — First", "")
		if err != nil || ms == 0 {
			t.Fatalf("CreateMilestone = %d, %v", ms, err)
		}
		k := mustCreate(t, j, "title", "hello\n\n- a\n- b", []string{"bug", "bug", "p1"}, "M01 — First")
		is, err := j.Get(ctx, k, false)
		if err != nil {
			t.Fatal(err)
		}
		if is.Title != "title" || is.State != "open" || is.Milestone != "M01 — First" || len(is.Comments) != 0 {
			t.Fatalf("Get = %+v", is)
		}
		if is.Body != "hello\n\n- a\n- b" {
			t.Fatalf("Body = %q", is.Body)
		}
		if !slices.Equal(is.Labels, []string{"bug", "p1"}) {
			t.Fatalf("Labels = %v", is.Labels)
		}
		nt, nb := "renamed", "new body"
		if err := j.Edit(ctx, k, IssueEdit{Title: &nt, Body: &nb, AddLabels: []string{"x"}, RemoveLabels: []string{"bug"}, RemoveMilestone: true}); err != nil {
			t.Fatal(err)
		}
		is, _ = j.Get(ctx, k, false)
		if is.Title != "renamed" || is.Body != "new body" || is.Milestone != "" || !slices.Equal(is.Labels, []string{"p1", "x"}) {
			t.Fatalf("after edit = %+v", is)
		}
		if err := j.Edit(ctx, k, IssueEdit{Milestone: "M01 — First"}); err != nil {
			t.Fatal(err)
		}
		if is, _ = j.Get(ctx, k, false); is.Milestone != "M01 — First" {
			t.Fatalf("milestone = %q", is.Milestone)
		}
		if err := j.Edit(ctx, k, IssueEdit{}); err != nil {
			t.Fatalf("empty edit: %v", err)
		}
	})
}

func TestJiraLabels(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		k := mustCreate(t, j, "t", "", nil, "")
		if err := j.AddLabels(ctx, k, []string{"a", "b", "a"}, true); err != nil {
			t.Fatal(err)
		}
		if err := j.RemoveLabels(ctx, k, []string{"a"}); err != nil {
			t.Fatal(err)
		}
		if is, _ := j.Get(ctx, k, false); !slices.Equal(is.Labels, []string{"b"}) {
			t.Fatalf("Labels = %v", is.Labels)
		}
		if err := j.EnsureLabels(ctx, []string{"ok", "two words"}, false); err == nil {
			t.Fatal("EnsureLabels accepted a label with a space")
		}
		if err := j.EnsureLabels(ctx, []string{"ok"}, false); err != nil {
			t.Fatal(err)
		}
		if _, err := j.Create(ctx, "t", "", []string{"a b"}, ""); err == nil {
			t.Fatal("Create accepted a label with a space")
		}
		if err := j.AddLabels(ctx, k, []string{"a b"}, true); err == nil {
			t.Fatal("AddLabels accepted a label with a space")
		}
	})
}

func TestJiraGetBadKey(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		for _, k := range []IssueKey{"42", "nope", "PROJ-999"} {
			if _, err := j.Get(ctx, k, false); !IsKind(err, KindNotFound) {
				t.Errorf("Get(%q) = %v, want not found", k, err)
			}
		}
	})
}

func TestJiraAssignSelf(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		k := mustCreate(t, j, "t", "", nil, "")
		if is, _ := j.Get(ctx, k, false); len(is.Assignees) != 0 {
			t.Fatalf("assignees = %v", is.Assignees)
		}
		if err := j.AssignSelf(ctx, k); err != nil {
			t.Fatal(err)
		}
		is, _ := j.Get(ctx, k, false)
		if len(is.Assignees) != 1 || is.Assignees[0] == "" {
			t.Fatalf("assignees = %v", is.Assignees)
		}
		mine, err := j.List(ctx, ListFilter{State: "all", Mine: true})
		if err != nil || !slices.Equal(keysOf(mine), []IssueKey{k}) {
			t.Fatalf("Mine = %v, %v", keysOf(mine), err)
		}
	})
}

func TestJiraList(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		if _, err := j.CreateMilestone(ctx, "M01", ""); err != nil {
			t.Fatal(err)
		}
		a := mustCreate(t, j, "a", "", []string{"bug"}, "M01")
		b := mustCreate(t, j, "b", "", []string{"bug", "p1"}, "")
		c := mustCreate(t, j, "c", "", nil, "M01")
		if err := j.Close(ctx, c, "", ""); err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			name string
			f    ListFilter
			want []IssueKey
		}{
			{"default is open", ListFilter{}, []IssueKey{a, b}},
			{"open", ListFilter{State: "open"}, []IssueKey{a, b}},
			{"closed", ListFilter{State: "closed"}, []IssueKey{c}},
			{"all", ListFilter{State: "all"}, []IssueKey{a, b, c}},
			{"label", ListFilter{State: "all", Labels: []string{"bug"}}, []IssueKey{a, b}},
			{"two labels", ListFilter{State: "all", Labels: []string{"bug", "p1"}}, []IssueKey{b}},
			{"milestone", ListFilter{State: "all", Milestone: "M01"}, []IssueKey{a, c}},
			{"limit", ListFilter{State: "all", Limit: 2}, []IssueKey{a, b}},
		}
		for _, tc := range cases {
			got, err := j.List(ctx, tc.f)
			if err != nil || !slices.Equal(keysOf(got), tc.want) {
				t.Errorf("%s: got %v, %v; want %v", tc.name, keysOf(got), err, tc.want)
			}
		}
	})
}

func TestJiraListPaging(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{PageSize: 2}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		var want []IssueKey
		for i := range 5 {
			want = append(want, mustCreate(t, j, fmt.Sprintf("i%d", i), "", nil, ""))
		}
		got, err := j.List(ctx, ListFilter{State: "all"})
		if err != nil || !slices.Equal(keysOf(got), want) {
			t.Fatalf("List = %v, %v; want %v", keysOf(got), err, want)
		}
		if got, _ := j.List(ctx, ListFilter{State: "all", Limit: 3}); !slices.Equal(keysOf(got), want[:3]) {
			t.Fatalf("Limit 3 = %v", keysOf(got))
		}
		searches := 0
		for _, r := range s.Requests() {
			if strings.Contains(r, "/search") {
				searches++
			}
		}
		if searches < 3 {
			t.Fatalf("only %d search requests; paging not exercised", searches)
		}
	})
}

func TestJiraCommentsLifecycle(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{PageSize: 2}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		k := mustCreate(t, j, "t", "", nil, "")
		id1, err := j.AddComment(ctx, k, "first")
		if err != nil || id1 == "" {
			t.Fatalf("AddComment = %q, %v", id1, err)
		}
		hid := human(s, k, "from a person")
		id3, _ := j.AddComment(ctx, k, "third")
		id4, _ := j.AddComment(ctx, k, "fourth")
		cs, err := j.Comments(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		var ids, bodies []string
		for _, c := range cs {
			ids, bodies = append(ids, c.ID), append(bodies, c.Body)
		}
		if !slices.Equal(ids, []string{id1, hid, id3, id4}) || !slices.Equal(bodies, []string{"first", "from a person", "third", "fourth"}) {
			t.Fatalf("Comments = %v %v", ids, bodies)
		}
		if cs[1].Author != "pat" {
			t.Fatalf("human author = %q", cs[1].Author)
		}
		if err := j.EditComment(ctx, k, id1, "edited"); err != nil {
			t.Fatal(err)
		}
		if err := j.DeleteComment(ctx, k, id3); err != nil {
			t.Fatal(err)
		}
		is, err := j.Get(ctx, k, true)
		if err != nil || len(is.Comments) != 3 || is.Comments[0].Body != "edited" || is.Comments[2].ID != id4 {
			t.Fatalf("Get with comments = %+v, %v", is.Comments, err)
		}
		if u, _ := j.CommentURL(ctx, k, id1); !strings.Contains(u, "focusedCommentId="+id1) {
			t.Fatalf("CommentURL = %q", u)
		}
	})
}

const (
	proofBody    = "<!-- rota:proof -->\n## Proof\n- row"
	handoffBody  = "<!-- rota:handoff ben@18 -->\ntext"
	feedbackBody = "<!-- rota:comment feedback -->\nhello"
)

func TestJiraMarkerCommentsRoundTrip(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		k := mustCreate(t, j, "t", "", nil, "")
		bodies := []string{proofBody, handoffBody, feedbackBody}
		var ids []string
		for _, b := range bodies {
			id, err := j.AddComment(ctx, k, b)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		check := func(want []string) {
			t.Helper()
			cs, err := j.Comments(ctx, k)
			if err != nil || len(cs) != len(want) {
				t.Fatalf("Comments = %+v, %v", cs, err)
			}
			for i, w := range want {
				if cs[i].Body != w {
					t.Errorf("comment %d = %q, want %q", i, cs[i].Body, w)
				}
			}
			is, _ := j.Get(ctx, k, true)
			for i, w := range want {
				if is.Comments[i].Body != w {
					t.Errorf("Get comment %d = %q, want %q", i, is.Comments[i].Body, w)
				}
			}
		}
		check(bodies)
		for _, raw := range s.CommentBodies(string(k)) {
			if strings.Contains(raw, "<!--") || strings.Contains(raw, "rota:") {
				t.Errorf("marker in stored comment body: %s", raw)
			}
		}
		// marker -> other marker body
		bodies[0] = "<!-- rota:proof -->\n## Proof\n- changed"
		if err := j.EditComment(ctx, k, ids[0], bodies[0]); err != nil {
			t.Fatal(err)
		}
		check(bodies)
		// marker -> plain: the property goes
		if err := j.EditComment(ctx, k, ids[1], "plain now"); err != nil {
			t.Fatal(err)
		}
		bodies[1] = "plain now"
		check(bodies)
		if _, ok := s.Property(string(k), propComment+ids[1]); ok {
			t.Error("property survived an edit to a plain body")
		}
		// delete drops the property
		if _, ok := s.Property(string(k), propComment+ids[2]); !ok {
			t.Fatal("no property for a marker comment")
		}
		if err := j.DeleteComment(ctx, k, ids[2]); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.Property(string(k), propComment+ids[2]); ok {
			t.Error("property survived DeleteComment")
		}
		check(bodies[:2])
	})
}

func TestJiraLongMarkerComment(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		k := mustCreate(t, j, "t", "", nil, "")
		long := "<!-- rota:proof -->\n" + strings.Repeat("héllo wörld 日本語 ", 6000)
		if len(long) < 2*propChunk {
			t.Fatalf("test body only %d bytes", len(long))
		}
		id, err := j.AddComment(ctx, k, long)
		if err != nil {
			t.Fatal(err)
		}
		cs, err := j.Comments(ctx, k)
		if err != nil || len(cs) != 1 || cs[0].Body != long {
			t.Fatalf("long body did not round-trip (err %v, %d comments)", err, len(cs))
		}
		if _, ok := s.Property(string(k), propComment+id+".2"); !ok {
			t.Error("body did not span 3 properties")
		}
		// shrinking drops the extra parts
		if err := j.EditComment(ctx, k, id, proofBody); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.Property(string(k), propComment+id+".1"); ok {
			t.Error("stale part survived a shorter edit")
		}
		if cs, _ = j.Comments(ctx, k); cs[0].Body != proofBody {
			t.Fatalf("after shrink = %q", cs[0].Body)
		}
		if err := j.DeleteComment(ctx, k, id); err != nil {
			t.Fatal(err)
		}
		if _, ok := s.Property(string(k), propComment+id); ok {
			t.Error("head property survived DeleteComment")
		}
	})
}

func TestJiraFieldsBlockRoundTrip(t *testing.T) {
	ctx := context.Background()
	block := marker.FieldsBlock([]string{"Priority: P1", "Area: tracker"})
	body := "Some description.\n\n" + block
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		k := mustCreate(t, j, "t", body, nil, "")
		is, err := j.Get(ctx, k, false)
		if err != nil || is.Body != body {
			t.Fatalf("Get body = %q, %v; want %q", is.Body, err, body)
		}
		if d := s.Description(string(k)); strings.Contains(d, "<!--") || strings.Contains(d, "rota:") {
			t.Fatalf("stored description has a marker: %s", d)
		}
		body2 := "Edited.\n\n" + marker.FieldsBlock([]string{"Priority: P2"})
		if err := j.Edit(ctx, k, IssueEdit{Body: &body2}); err != nil {
			t.Fatal(err)
		}
		if is, _ = j.Get(ctx, k, false); is.Body != body2 {
			t.Fatalf("after edit body = %q, want %q", is.Body, body2)
		}
		if d := s.Description(string(k)); strings.Contains(d, "<!--") {
			t.Fatalf("stored description has a marker: %s", d)
		}
		// a body without a block clears the stored one
		plain := "just text"
		if err := j.Edit(ctx, k, IssueEdit{Body: &plain}); err != nil {
			t.Fatal(err)
		}
		if is, _ = j.Get(ctx, k, false); is.Body != plain {
			t.Fatalf("plain body = %q", is.Body)
		}
		// a fields block alone round-trips through List too
		k2 := mustCreate(t, j, "only fields", block, nil, "")
		got, err := j.List(ctx, ListFilter{})
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range got {
			if i.Key == k2 && i.Body != block {
				t.Fatalf("List body = %q, want %q", i.Body, block)
			}
		}
	})
}

func TestJiraNoMarkerInCommentBodies(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		fields := marker.FieldsBlock([]string{"Priority: P1"})
		k := mustCreate(t, j, "t", "text\n\n"+fields, nil, "")
		if err := j.PutClaim(ctx, k, "ben@18"); err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, b := range []string{proofBody, handoffBody, feedbackBody, "<!-- rota:claim ben@18 -->", "plain"} {
			id, err := j.AddComment(ctx, k, b)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := j.EditComment(ctx, k, ids[0], "<!-- rota:proof -->\nnew"); err != nil {
			t.Fatal(err)
		}
		if err := j.Close(ctx, k, "not_planned", "<!-- rota:comment closing -->\nbye"); err != nil {
			t.Fatal(err)
		}
		body := "again\n\n" + fields
		if err := j.Edit(ctx, k, IssueEdit{Body: &body}); err != nil {
			t.Fatal(err)
		}
		raws := append(s.CommentBodies(string(k)), s.Description(string(k)))
		if len(raws) != 8 {
			t.Fatalf("got %d raw bodies, want 7 comments (a claim among them) and a description", len(raws))
		}
		for _, raw := range raws {
			if strings.Contains(raw, "<!--") || strings.Contains(raw, "rota:") {
				t.Errorf("marker leaked into a raw body: %s", raw)
			}
		}
	})
}

var germanStatuses = []jiratest.Status{{ID: "1", Name: "Offen", Category: "new"}, {ID: "2", Name: "In Arbeit", Category: "indeterminate"}, {ID: "3", Name: "Erledigt", Category: "done"}}

func TestJiraCloseReopenByCategory(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name       string
		o          jiratest.Options
		todo, done string
	}{
		{"default", jiratest.Options{}, "To Do", "Done"},
		{"german", jiratest.Options{Statuses: germanStatuses}, "Offen", "Erledigt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eachJira(t, tc.o, func(t *testing.T, j *Jira, s *jiratest.Server) {
				k := mustCreate(t, j, "t", "", nil, "")
				if err := j.Close(ctx, k, "completed", "all done"); err != nil {
					t.Fatal(err)
				}
				if got := s.StatusOf(string(k)); got != tc.done {
					t.Fatalf("status after Close = %q, want %q", got, tc.done)
				}
				is, _ := j.Get(ctx, k, true)
				if is.State != "closed" || is.StateReason != "completed" || is.ClosedAt == "" {
					t.Fatalf("closed issue = %+v", is)
				}
				if len(is.Comments) != 1 || is.Comments[0].Body != "all done" {
					t.Fatalf("close comment = %+v", is.Comments)
				}
				if err := j.Reopen(ctx, k); err != nil {
					t.Fatal(err)
				}
				if got := s.StatusOf(string(k)); got != tc.todo {
					t.Fatalf("status after Reopen = %q, want %q", got, tc.todo)
				}
				if is, _ = j.Get(ctx, k, false); is.State != "open" || is.StateReason != "" {
					t.Fatalf("reopened issue = %+v", is)
				}

				if err := j.Close(ctx, k, "not_planned", ""); err != nil {
					t.Fatal(err)
				}
				is, _ = j.Get(ctx, k, false)
				if is.StateReason != "not_planned" || !slices.Contains(is.Labels, "not-planned") {
					t.Fatalf("not planned close = %+v", is)
				}
				if err := j.Reopen(ctx, k); err != nil {
					t.Fatal(err)
				}
				if is, _ = j.Get(ctx, k, false); slices.Contains(is.Labels, "not-planned") || is.State != "open" {
					t.Fatalf("reopen kept the label: %+v", is)
				}
			})
		})
	}
}

func TestJiraCloseClosedIsNoop(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		k := mustCreate(t, j, "t", "", nil, "")
		if err := j.Close(ctx, k, "", ""); err != nil {
			t.Fatal(err)
		}
		count := func() (n int) {
			for _, r := range s.Requests() {
				if strings.HasPrefix(r, "POST ") && strings.Contains(r, "/transitions") {
					n++
				}
			}
			return
		}
		before := count()
		if before != 1 {
			t.Fatalf("first Close posted %d transitions", before)
		}
		if err := j.Close(ctx, k, "", ""); err != nil {
			t.Fatal(err)
		}
		if after := count(); after != before {
			t.Fatalf("closing a closed issue posted %d more transitions", after-before)
		}
		if err := j.Reopen(ctx, k); err != nil {
			t.Fatal(err)
		}
		before = count()
		if err := j.Reopen(ctx, k); err != nil {
			t.Fatal(err)
		}
		if after := count(); after != before {
			t.Fatal("reopening an open issue posted a transition")
		}
	})
}

func TestJiraMilestones(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		id1, err := j.CreateMilestone(ctx, "M07 — Title", "first")
		if err != nil || id1 == 0 {
			t.Fatalf("CreateMilestone = %d, %v", id1, err)
		}
		id2, err := j.CreateMilestone(ctx, "M08 — Other", "")
		if err != nil || id2 == id1 {
			t.Fatalf("second CreateMilestone = %d, %v", id2, err)
		}
		titles := func(state string) []string {
			ms, err := j.Milestones(ctx, state)
			if err != nil {
				t.Fatal(err)
			}
			out := []string{}
			for _, m := range ms {
				out = append(out, m.Title)
			}
			return out
		}
		if got := titles("open"); !slices.Equal(got, []string{"M07 — Title", "M08 — Other"}) {
			t.Fatalf("open = %v", got)
		}
		if got, ok, err := j.FindMilestone(ctx, "M07"); err != nil || !ok || got != "M07 — Title" {
			t.Fatalf("FindMilestone M07 = %q, %v, %v", got, ok, err)
		}
		if _, ok, _ := j.FindMilestone(ctx, "M0"); ok {
			t.Fatal("FindMilestone matched a partial token")
		}
		if _, ok, _ := j.FindMilestone(ctx, "M99"); ok {
			t.Fatal("FindMilestone found a missing id")
		}
		title, desc, closed := "M07 — Renamed", "described", "closed"
		if err := j.EditMilestone(ctx, id1, MilestoneEdit{Title: &title, Description: &desc, State: &closed}); err != nil {
			t.Fatal(err)
		}
		ms, _ := j.Milestones(ctx, "closed")
		if len(ms) != 1 || ms[0].Number != id1 || ms[0].Title != title || ms[0].Description != desc || ms[0].State != "closed" {
			t.Fatalf("closed = %+v", ms)
		}
		if got := titles("open"); !slices.Equal(got, []string{"M08 — Other"}) {
			t.Fatalf("open after close = %v", got)
		}
		if got := titles("all"); len(got) != 2 {
			t.Fatalf("all = %v", got)
		}
		if got, ok, _ := j.FindMilestone(ctx, "M07"); !ok || got != title {
			t.Fatalf("FindMilestone of a closed one = %q, %v", got, ok)
		}
		open := "open"
		if err := j.EditMilestone(ctx, id1, MilestoneEdit{State: &open}); err != nil {
			t.Fatal(err)
		}
		if got := titles("open"); len(got) != 2 {
			t.Fatalf("open after reopen = %v", got)
		}
		if err := j.EditMilestone(ctx, id1, MilestoneEdit{}); err != nil {
			t.Fatalf("empty EditMilestone: %v", err)
		}

		a := mustCreate(t, j, "a", "", nil, "M08 — Other")
		b := mustCreate(t, j, "b", "", nil, "")
		if err := j.Edit(ctx, b, IssueEdit{Milestone: "M08 — Other"}); err != nil {
			t.Fatal(err)
		}
		c := mustCreate(t, j, "c", "", nil, title)
		if is, _ := j.Get(ctx, b, false); is.Milestone != "M08 — Other" {
			t.Fatalf("Milestone = %q", is.Milestone)
		}
		if err := j.Close(ctx, b, "", ""); err != nil {
			t.Fatal(err)
		}
		for state, want := range map[string][]IssueKey{"all": {a, b}, "": {a, b}, "open": {a}, "closed": {b}} {
			got, err := j.IssuesInMilestone(ctx, "M08 — Other", state)
			if err != nil || !slices.Equal(keysOf(got), want) {
				t.Errorf("IssuesInMilestone(%q) = %v, %v; want %v", state, keysOf(got), err, want)
			}
		}
		_ = c
	})
}

func TestJiraRetriesRateLimit(t *testing.T) {
	ctx := context.Background()
	eachJira(t, jiratest.Options{}, func(t *testing.T, j *Jira, s *jiratest.Server) {
		k := mustCreate(t, j, "t", "", nil, "")
		var waits []time.Duration
		j.rest.Sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
		s.RateLimitNext(1, "1")
		got, err := j.List(ctx, ListFilter{})
		if err != nil || !slices.Equal(keysOf(got), []IssueKey{k}) {
			t.Fatalf("List through a 429 = %v, %v", keysOf(got), err)
		}
		if !slices.Equal(waits, []time.Duration{time.Second}) {
			t.Fatalf("waits = %v, want the Retry-After of 1s", waits)
		}
	})
}
