package tracker

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// scriptedCall is one expected forge call and what it answers.
type scriptedCall struct {
	name   string
	argv   []string
	stdin  string
	stdout string
	stderr string
	code   int
}

// scriptedAdapter is an adapter for provider whose forge CLI answers calls in order
// and fails the test on any call that is not next in the script.
func scriptedAdapter(t *testing.T, provider string, calls ...scriptedCall) Adapter {
	t.Helper()
	x := func(_ context.Context, _, name string, args []string, stdin []byte) ([]byte, []byte, int, error) {
		if len(calls) == 0 {
			t.Fatalf("unexpected call %s %q", name, args)
		}
		c := calls[0]
		calls = calls[1:]
		if name != c.name || !reflect.DeepEqual(args, c.argv) || string(stdin) != c.stdin {
			t.Fatalf("call\n got %s %q stdin %q\nwant %s %q stdin %q", name, args, stdin, c.name, c.argv, c.stdin)
		}
		return []byte(c.stdout), []byte(c.stderr), c.code, nil
	}
	a, err := New(context.Background(), Settings{Provider: provider}, "", "",
		WithExec(x, func(string) (string, error) { return "/fake", nil }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if len(calls) != 0 {
			t.Errorf("%d scripted calls not made, next %q", len(calls), calls[0].argv)
		}
	})
	return a
}

func TestPRCreate(t *testing.T) {
	spec := PRSpec{Title: "My title", Body: "Summary", Head: "feat/x", Base: "main"}
	gh := scriptedAdapter(t, "github", scriptedCall{name: "gh", argv: []string{"pr", "create", "--title", "My title", "--body-file", "-"},
		stdin: "Summary", stdout: "warning\nhttps://github.com/o/r/pull/3\n"})
	if gh.PRNeedsBase() {
		t.Error("github PRs take no base")
	}
	if url, err := gh.PRCreate(context.Background(), spec); err != nil || url != "https://github.com/o/r/pull/3" {
		t.Errorf("github: %q %v", url, err)
	}

	gl := scriptedAdapter(t, "gitlab", scriptedCall{name: "glab", argv: []string{"mr", "create", "--title", "My title", "--description", "Summary",
		"--source-branch", "feat/x", "--target-branch", "main", "--yes"}, stdout: "https://gitlab.com/o/r/-/merge_requests/3\n"})
	if !gl.PRNeedsBase() {
		t.Error("gitlab MRs need a base")
	}
	if url, err := gl.PRCreate(context.Background(), spec); err != nil || url != "https://gitlab.com/o/r/-/merge_requests/3" {
		t.Errorf("gitlab: %q %v", url, err)
	}
}

func TestPRCreateFailure(t *testing.T) {
	// A failure is the first stderr line, and never KindNotFound even when the
	// forge's wording matches the not-found sniffer.
	a := scriptedAdapter(t, "github", scriptedCall{name: "gh", argv: []string{"pr", "create", "--title", "T", "--body-file", "-"},
		stdin: "B", stderr: "\nHTTP 404 not found\nsecond line\n", code: 1})
	_, err := a.PRCreate(context.Background(), PRSpec{Title: "T", Body: "B"})
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindFailed || e.Message != "gh pr create exited 1: HTTP 404 not found" {
		t.Errorf("got %#v", err)
	}
}

func TestReleaseViewGitHub(t *testing.T) {
	view := []string{"release", "view", "v1.0.0", "--json", "isDraft,assets"}
	for _, tc := range []struct {
		name string
		call scriptedCall
		want Release
		err  string
	}{
		{"draft", scriptedCall{name: "gh", argv: view, stdout: `{"isDraft":true,"assets":[{"name":"a"},{"name":"b"}]}`},
			Release{Found: true, IsDraft: true, Assets: []string{"a", "b"}}, ""},
		{"published", scriptedCall{name: "gh", argv: view, stdout: `{"isDraft":false,"assets":[]}`}, Release{Found: true}, ""},
		{"none", scriptedCall{name: "gh", argv: view, stderr: "release not found", code: 1}, Release{}, ""},
		{"failed", scriptedCall{name: "gh", argv: view, stderr: "boom\n", code: 1}, Release{}, "gh release view failed: boom"},
		{"garbled", scriptedCall{name: "gh", argv: view, stdout: "not json"}, Release{}, "gh release view: unreadable output: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := scriptedAdapter(t, "github", tc.call)
			rel, checked, err := a.ReleaseView(context.Background(), "v1.0.0")
			if !checked {
				t.Error("github looks the release up")
			}
			if tc.err != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tc.err) {
					t.Errorf("error %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(rel, tc.want) {
				t.Errorf("got %+v %v, want %+v", rel, err, tc.want)
			}
		})
	}
}

func TestReleaseGitHubWrites(t *testing.T) {
	spec := ReleaseSpec{Tag: "v1.0.0", Title: "One", Notes: "/tmp/n.md"}
	a := scriptedAdapter(t, "github",
		scriptedCall{name: "gh", argv: []string{"release", "create", "v1.0.0", "--title", "One", "--notes-file", "/tmp/n.md"}, stdout: "https://x/1\n"},
		scriptedCall{name: "gh", argv: []string{"release", "create", "v1.0.0", "--title", "One", "--notes-file", "/tmp/n.md", "--draft"}, stdout: "https://x/2\n"},
		scriptedCall{name: "gh", argv: []string{"release", "edit", "v1.0.0", "--title", "One", "--notes-file", "/tmp/n.md", "--draft=false"}, stdout: "https://x/3\n"},
		scriptedCall{name: "gh", argv: []string{"release", "create", "v1.0.0", "--title", "One", "--notes-file", "/tmp/n.md"}, stderr: "no token\n", code: 4},
	)
	if !a.ReleaseDrafts() {
		t.Error("github has draft releases")
	}
	ctx := context.Background()
	if u, err := a.ReleaseCreate(ctx, spec); err != nil || u != "https://x/1" {
		t.Errorf("create: %q %v", u, err)
	}
	spec.Draft = true
	if u, err := a.ReleaseCreate(ctx, spec); err != nil || u != "https://x/2" {
		t.Errorf("draft create: %q %v", u, err)
	}
	spec.Draft = false
	if u, err := a.ReleaseEdit(ctx, spec); err != nil || u != "https://x/3" {
		t.Errorf("edit: %q %v", u, err)
	}
	if _, err := a.ReleaseCreate(ctx, spec); err == nil || err.Error() != "gh release create failed: no token" {
		t.Errorf("failure: %v", err)
	}
}

func TestReleaseGitLab(t *testing.T) {
	// GitLab is never asked about a release, and has no drafts or edit.
	a := scriptedAdapter(t, "gitlab",
		scriptedCall{name: "glab", argv: []string{"release", "create", "v1.0.0", "--name", "One", "--notes-file", "/tmp/n.md"}, stdout: "created\nhttps://x/1\n"},
		scriptedCall{name: "glab", argv: []string{"release", "create", "v1.0.0", "--name", "One", "--notes-file", "/tmp/n.md"}, stderr: "403\n", code: 1},
	)
	ctx := context.Background()
	if a.ReleaseDrafts() {
		t.Error("gitlab has no draft releases")
	}
	if rel, checked, err := a.ReleaseView(ctx, "v1.0.0"); checked || err != nil || rel.Found {
		t.Errorf("view: %+v %v %v", rel, checked, err)
	}
	spec := ReleaseSpec{Tag: "v1.0.0", Title: "One", Notes: "/tmp/n.md"}
	if u, err := a.ReleaseCreate(ctx, spec); err != nil || u != "https://x/1" {
		t.Errorf("create: %q %v", u, err)
	}
	if _, err := a.ReleaseCreate(ctx, spec); err == nil || err.Error() != "glab release create failed: 403" {
		t.Errorf("failure: %v", err)
	}
	if _, err := a.ReleaseEdit(ctx, spec); err == nil {
		t.Error("gitlab cannot edit a release")
	}
}
