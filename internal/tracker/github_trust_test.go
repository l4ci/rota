package tracker

import (
	"context"
	"strings"
	"testing"
)

func githubTrust(t *testing.T, s *scripted) *GitHub {
	t.Helper()
	g, ok := newAdapter(t, "github", s).(*GitHub)
	if !ok {
		t.Fatal("not a GitHub adapter")
	}
	return g
}

func TestGitHubIssueEditors(t *testing.T) {
	s := &scripted{answer: func(string, []string) (string, string, int) {
		return `{"data":{"repository":{"issue":{"author":{"login":"ann"},"lastEditedBy":{"login":"bob"}}}}}`, "", 0
	}}
	a, e, err := githubTrust(t, s).IssueEditors(context.Background(), NumKey(899))
	if err != nil || a != "ann" || e != "bob" {
		t.Fatalf("%q %q %v", a, e, err)
	}
	for _, want := range []string{"api graphql", "-F owner={owner}", "-F repo={repo}", "-F n=899", "lastEditedBy{login}"} {
		if !strings.Contains(s.calls[0], want) {
			t.Errorf("call lacks %q: %s", want, s.calls[0])
		}
	}
}

func TestGitHubIssueEditorsNullsAreEmpty(t *testing.T) {
	s := &scripted{answer: func(string, []string) (string, string, int) {
		return `{"data":{"repository":{"issue":{"author":null,"lastEditedBy":null}}}}`, "", 0
	}}
	a, e, err := githubTrust(t, s).IssueEditors(context.Background(), NumKey(1))
	if err != nil || a != "" || e != "" {
		t.Fatalf("%q %q %v", a, e, err)
	}
}

func TestGitHubIssueEditorsErrors(t *testing.T) {
	g := githubTrust(t, &scripted{answer: func(string, []string) (string, string, int) { return "", "boom", 1 }})
	if _, _, err := g.IssueEditors(context.Background(), NumKey(1)); err == nil {
		t.Error("a failed call is not an error")
	}
	if _, _, err := g.IssueEditors(context.Background(), IssueKey("PROJ-1")); err == nil {
		t.Error("a non-numeric key is not an error")
	}
}

func TestGitHubCanPush(t *testing.T) {
	for perm, want := range map[string]bool{"admin": true, "maintain": true, "write": true, "triage": false, "read": false, "none": false} {
		s := &scripted{answer: func(string, []string) (string, string, int) { return `{"permission":"` + perm + `"}`, "", 0 }}
		got, err := githubTrust(t, s).CanPush(context.Background(), "ann")
		if err != nil || got != want {
			t.Errorf("%s: %v %v", perm, got, err)
		}
		if !strings.Contains(s.calls[0], "repos/{owner}/{repo}/collaborators/ann/permission") {
			t.Errorf("call %q", s.calls[0])
		}
	}
}

func TestGitHubCanPushRejectsOddLogins(t *testing.T) {
	s := &scripted{answer: func(string, []string) (string, string, int) { return `{"permission":"admin"}`, "", 0 }}
	g := githubTrust(t, s)
	for _, bad := range []string{"", "a/b", "a b", "../x", "a?x=1", "a%2f", "[bot]", "ann[bot]x"} {
		if ok, err := g.CanPush(context.Background(), bad); err == nil || ok {
			t.Errorf("login %q accepted", bad)
		}
	}
	if len(s.calls) != 0 {
		t.Errorf("a rejected login reached the forge: %q", s.calls)
	}
	if ok, err := g.CanPush(context.Background(), "dependabot[bot]"); err != nil || !ok {
		t.Errorf("bot login: %v %v", ok, err)
	}
}

func TestGitHubCanPushFailureIsAnError(t *testing.T) {
	g := githubTrust(t, &scripted{answer: func(string, []string) (string, string, int) { return "", "HTTP 404", 1 }})
	if ok, err := g.CanPush(context.Background(), "ann"); err == nil || ok {
		t.Errorf("%v %v", ok, err)
	}
}
