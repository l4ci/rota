package tracker

import (
	"context"
	"strings"
	"testing"
)

func TestGitLabMRNotes(t *testing.T) {
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		return `[{"id":7,"body":"first","author":{"username":"ann"},"system":false},` +
			`{"id":8,"body":"added 1 commit","system":true},` +
			`{"id":9,"body":"m: go","author":{"username":"bob"},"system":false}]`, "", 0
	}}
	a := newAdapter(t, "gitlab", s)
	got, err := a.MRNotes(context.Background(), 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "7" || got[1].ID != "9" || got[1].Author != "bob" || got[1].Body != "m: go" {
		t.Fatalf("notes %+v", got)
	}
	if !strings.Contains(s.calls[0], "projects/:id/merge_requests/12/notes?sort=asc&order_by=created_at") {
		t.Errorf("call %q", s.calls[0])
	}
}

func TestGitLabAddMRNote(t *testing.T) {
	s := &scripted{answer: func(string, []string) (string, string, int) { return `{"id":41,"body":"x"}`, "", 0 }}
	id, err := newAdapter(t, "gitlab", s).AddMRNote(context.Background(), 12, "hello")
	if err != nil || id != "41" {
		t.Fatalf("%q %v", id, err)
	}
	if want := "projects/:id/merge_requests/12/notes -f body=hello"; !strings.Contains(s.calls[0], "-X POST") || !strings.Contains(s.calls[0], want) {
		t.Errorf("call %q", s.calls[0])
	}
}

func TestGitHubPRThreadIsIssueComments(t *testing.T) {
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		if strings.Contains(strings.Join(args, " "), "-X POST") {
			return `{"id":5}`, "", 0
		}
		return `[{"id":5,"body":"b","user":{"login":"u"}}]`, "", 0
	}}
	a := newAdapter(t, "github", s)
	got, err := a.MRNotes(context.Background(), 3)
	if err != nil || len(got) != 1 || got[0].ID != "5" {
		t.Fatalf("%+v %v", got, err)
	}
	if id, err := a.AddMRNote(context.Background(), 3, "x"); err != nil || id != "5" {
		t.Fatalf("%q %v", id, err)
	}
	for _, c := range s.calls {
		if !strings.Contains(c, "repos/{owner}/{repo}/issues/3/comments") {
			t.Errorf("call %q is not the issue comments API", c)
		}
	}
}

func TestCommentURL(t *testing.T) {
	gh := &scripted{answer: func(string, []string) (string, string, int) {
		return `{"id":5,"html_url":"https://github.com/o/r/issues/3#issuecomment-5"}`, "", 0
	}}
	if u, err := newAdapter(t, "github", gh).CommentURL(context.Background(), false, 3, "5"); err != nil || u != "https://github.com/o/r/issues/3#issuecomment-5" {
		t.Errorf("github %q %v", u, err)
	}
	if !strings.Contains(gh.calls[0], "repos/{owner}/{repo}/issues/comments/5") {
		t.Errorf("call %q", gh.calls[0])
	}
	gl := &scripted{answer: func(string, []string) (string, string, int) {
		return `{"iid":12,"web_url":"https://gitlab.com/o/r/-/merge_requests/12"}`, "", 0
	}}
	if u, err := newAdapter(t, "gitlab", gl).CommentURL(context.Background(), true, 12, "41"); err != nil || u != "https://gitlab.com/o/r/-/merge_requests/12#note_41" {
		t.Errorf("gitlab %q %v", u, err)
	}
	if !strings.Contains(gl.calls[0], "mr view 12") {
		t.Errorf("call %q", gl.calls[0])
	}
}
