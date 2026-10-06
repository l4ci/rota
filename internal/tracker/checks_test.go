package tracker

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestGitHubCommitChecks(t *testing.T) {
	s := &scripted{answer: func(name string, args []string) (string, string, int) {
		// The CLI appends --paginate to every GET api call.
		if name != "gh" || len(args) != 3 || args[0] != "api" || args[2] != "--paginate" {
			t.Fatalf("unexpected command: %s %q", name, args)
		}
		switch args[1] {
		case "repos/{owner}/{repo}/commits/abc123/check-runs?per_page=100":
			return `{"check_runs":[
				{"name":"build","status":"completed","conclusion":"success","html_url":"u1"},
				{"name":"lint","status":"completed","conclusion":"neutral","html_url":"u2"},
				{"name":"docs","status":"completed","conclusion":"skipped","html_url":"u3"},
				{"name":"test","status":"completed","conclusion":"failure","html_url":"u4"},
				{"name":"slow","status":"completed","conclusion":"timed_out","html_url":"u5"},
				{"name":"stop","status":"completed","conclusion":"cancelled","html_url":"u6"},
				{"name":"odd","status":"completed","conclusion":"","html_url":"u7"},
				{"name":"queued","status":"queued","conclusion":null,"html_url":"u8"},
				{"name":"busy","status":"in_progress","conclusion":null,"html_url":"u9"}]}`, "", 0
		case "repos/{owner}/{repo}/commits/abc123/status":
			return `{"statuses":[
				{"context":"ci/a","state":"success","target_url":"s1"},
				{"context":"ci/b","state":"pending","target_url":"s2"},
				{"context":"ci/c","state":"failure","target_url":"s3"},
				{"context":"ci/d","state":"error","target_url":"s4"}]}`, "", 0
		}
		t.Fatalf("unexpected path: %q", args[1])
		return "", "", 1
	}}
	got, err := newAdapter(t, "github", s).CommitChecks(context.Background(), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	want := []CheckRun{
		{"build", CheckSuccess, "u1"}, {"lint", CheckSuccess, "u2"}, {"docs", CheckSuccess, "u3"},
		{"test", CheckFailure, "u4"}, {"slow", CheckFailure, "u5"}, {"stop", CheckFailure, "u6"},
		{"odd", CheckFailure, "u7"}, {"queued", CheckPending, "u8"}, {"busy", CheckPending, "u9"},
		{"ci/a", CheckSuccess, "s1"}, {"ci/b", CheckPending, "s2"},
		{"ci/c", CheckFailure, "s3"}, {"ci/d", CheckFailure, "s4"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("checks %+v, want %+v", got, want)
	}
}

func TestGitHubCommitChecksPaginated(t *testing.T) {
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		if strings.Contains(args[1], "/status") {
			return `{"statuses":[]}`, "", 0
		}
		return `{"check_runs":[{"name":"a","status":"completed","conclusion":"success","html_url":"1"}]}` + "\n" +
			`{"check_runs":[{"name":"b","status":"queued","html_url":"2"}]}`, "", 0
	}}
	got, err := newAdapter(t, "github", s).CommitChecks(context.Background(), "abc123")
	want := []CheckRun{{"a", CheckSuccess, "1"}, {"b", CheckPending, "2"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("checks %+v, error %v; want %+v", got, err, want)
	}
}

func TestGitHubCommitChecksEmpty(t *testing.T) {
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		if strings.HasSuffix(args[1], "/status") {
			return `{"statuses":[]}`, "", 0
		}
		return `{"check_runs":[]}`, "", 0
	}}
	got, err := newAdapter(t, "github", s).CommitChecks(context.Background(), "abc123")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("checks %#v, error %v; want an empty list", got, err)
	}
}

func TestGitLabCommitChecks(t *testing.T) {
	s := &scripted{answer: func(name string, args []string) (string, string, int) {
		if name != "glab" || len(args) < 2 || args[0] != "api" || args[1] != "projects/:id/pipelines?sha=abc123&per_page=100" {
			t.Fatalf("unexpected command: %s %q", name, args)
		}
		return `[
			{"id":5,"status":"failed","ref":"main","web_url":"m5"},
			{"id":9,"status":"success","ref":"main","web_url":"m9"},
			{"id":7,"status":"running","ref":"feat","web_url":"f7"},
			{"id":3,"status":"canceled","ref":"zed","web_url":"z3"},
			{"id":4,"status":"manual","ref":"man","web_url":"n4"},
			{"id":2,"status":"skipped","ref":"skip","web_url":"k2"},
			{"id":1,"status":"weird","ref":"unk","web_url":"u1"}]`, "", 0
	}}
	got, err := newAdapter(t, "gitlab", s).CommitChecks(context.Background(), "abc123")
	if err != nil {
		t.Fatal(err)
	}
	want := []CheckRun{
		{"pipeline #7 (feat)", CheckPending, "f7"},
		{"pipeline #9 (main)", CheckSuccess, "m9"},
		{"pipeline #4 (man)", CheckFailure, "n4"},
		{"pipeline #2 (skip)", CheckFailure, "k2"},
		{"pipeline #1 (unk)", CheckPending, "u1"},
		{"pipeline #3 (zed)", CheckFailure, "z3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("checks %+v, want %+v", got, want)
	}
}

func TestGitLabCommitChecksEmpty(t *testing.T) {
	s := &scripted{answer: func(string, []string) (string, string, int) { return "[]", "", 0 }}
	got, err := newAdapter(t, "gitlab", s).CommitChecks(context.Background(), "abc123")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("checks %#v, error %v; want an empty list", got, err)
	}
}

func TestCommitChecksCLIFailure(t *testing.T) {
	for _, provider := range []string{"github", "gitlab"} {
		s := &scripted{answer: func(string, []string) (string, string, int) { return "", "boom", 1 }}
		got, err := newAdapter(t, provider, s).CommitChecks(context.Background(), "abc123")
		if err == nil || got != nil {
			t.Errorf("%s: checks %v, error %v; want an error", provider, got, err)
		}
	}
}
