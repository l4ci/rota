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

func TestGitHubIssueAuthorship(t *testing.T) {
	s := &scripted{answer: func(string, []string) (string, string, int) {
		return `{"data":{"repository":{"issue":{"author":{"login":"ann"},"editor":{"login":"bob"},"lastEditedAt":"2026-10-19T00:00:00Z","body":"## Try it\n- echo hi"}}}}`, "", 0
	}}
	a, err := githubTrust(t, s).IssueAuthorship(context.Background(), NumKey(899))
	if err != nil || a != (Authorship{Author: "ann", Editor: "bob", Edited: true, Body: "## Try it\n- echo hi"}) {
		t.Fatalf("%+v %v", a, err)
	}
	for _, want := range []string{"api graphql", "-F owner={owner}", "-F repo={repo}", "-F n=899", "editor{login}", "lastEditedAt", "body"} {
		if !strings.Contains(s.calls[0], want) {
			t.Errorf("call lacks %q: %s", want, s.calls[0])
		}
	}
}

func TestGitHubIssueAuthorshipNullsAreEmpty(t *testing.T) {
	s := &scripted{answer: func(string, []string) (string, string, int) {
		return `{"data":{"repository":{"issue":{"author":null,"editor":null}}}}`, "", 0
	}}
	a, err := githubTrust(t, s).IssueAuthorship(context.Background(), NumKey(1))
	if err != nil || a != (Authorship{}) {
		t.Fatalf("%+v %v", a, err)
	}
}

func TestGitHubIssueAuthorshipErrors(t *testing.T) {
	g := githubTrust(t, &scripted{answer: func(string, []string) (string, string, int) { return "", "boom", 1 }})
	if _, err := g.IssueAuthorship(context.Background(), NumKey(1)); err == nil {
		t.Error("a failed call is not an error")
	}
	if _, err := g.IssueAuthorship(context.Background(), IssueKey("PROJ-1")); err == nil {
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

// An edit by a deleted account has a lastEditedAt and no editor: edited is
// true with an empty editor, so the gate cannot mistake it for an unedited body.
func TestGitHubIssueAuthorshipDeletedEditor(t *testing.T) {
	s := &scripted{answer: func(string, []string) (string, string, int) {
		return `{"data":{"repository":{"issue":{"author":{"login":"ann"},"editor":null,"lastEditedAt":"2026-10-19T00:00:00Z"}}}}`, "", 0
	}}
	a, err := githubTrust(t, s).IssueAuthorship(context.Background(), NumKey(1))
	if err != nil || a != (Authorship{Author: "ann", Edited: true}) {
		t.Fatalf("%+v %v", a, err)
	}
}
