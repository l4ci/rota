package tracker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// editRig answers an issue-edit exchange from a mutable REST issue and keeps
// the argv and stdin of every call.
type editRig struct {
	calls  []string
	stdin  []string
	issue  string
	apply  bool // whether a PATCH changes the issue
	patchd string
}

func (r *editRig) exec(_ context.Context, _, name string, args []string, stdin []byte) ([]byte, []byte, int, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	r.stdin = append(r.stdin, string(stdin))
	switch {
	case strings.Contains(strings.Join(args, " "), "milestones"):
		return []byte(`[{"number":4,"title":"M1","state":"open"}]`), nil, 0, nil
	case args[0] == "api" && len(args) > 2 && (args[2] == "PATCH" || args[2] == "POST" || args[2] == "DELETE"):
		if r.apply {
			r.issue = r.patchd
		}
		return []byte(`{}`), nil, 0, nil
	}
	return []byte(r.issue), nil, 0, nil
}

func (r *editRig) adapter(t *testing.T) Adapter {
	t.Helper()
	a, err := New(context.Background(), Settings{Provider: "github"}, "", "/repo",
		WithExec(r.exec, func(string) (string, error) { return "/fake", nil }), WithSleep(func(time.Duration) {}))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestGitHubEditGoesOverRESTAndReadsBack pins the argv: gh 2.45 `issue edit`
// fails on the Projects (classic) GraphQL query, so title, body and milestone
// go through one PATCH whose JSON body is on stdin, and labels through the
// incremental endpoints: a PATCH of the whole set would lose a concurrent write.
func TestGitHubEditGoesOverRESTAndReadsBack(t *testing.T) {
	r := &editRig{apply: true,
		issue:  `{"title":"old","body":"b","labels":[{"name":"bug"},{"name":"p1"}],"milestone":null}`,
		patchd: `{"title":"new","body":"x\ny","labels":[{"name":"bug"},{"name":"extra"}],"milestone":{"title":"M1"}}`}
	title, body := "new", "x\ny"
	err := r.adapter(t).Edit(context.Background(), NumKey(7), IssueEdit{Title: &title, Body: &body,
		AddLabels: []string{"extra"}, RemoveLabels: []string{"p1"}, Milestone: "M1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"gh api repos/{owner}/{repo}/milestones?state=all&per_page=100 --paginate",
		"gh api -X PATCH repos/{owner}/{repo}/issues/7 --input -",
		"gh api -X POST repos/{owner}/{repo}/issues/7/labels --input -",
		"gh api -X DELETE repos/{owner}/{repo}/issues/7/labels/p1",
		"gh api repos/{owner}/{repo}/issues/7 --paginate",
	}
	if strings.Join(r.calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("calls\n%s\nwant\n%s", strings.Join(r.calls, "\n"), strings.Join(want, "\n"))
	}
	const patch = `{"body":"x\ny","milestone":4,"title":"new"}`
	if r.stdin[1] != patch {
		t.Fatalf("PATCH body %q, want %q", r.stdin[1], patch)
	}
	if want := `{"labels":["extra"]}`; r.stdin[2] != want {
		t.Fatalf("POST body %q, want %q", r.stdin[2], want)
	}
	for _, c := range r.calls {
		if strings.Contains(c, "issue edit") {
			t.Fatalf("issue edit still used: %s", c)
		}
	}
}

// TestGitHubEditErrorsWhenTheChangeIsNotThere: a write that exits 0 but leaves
// the issue as it was is an error naming what is missing, for each field the
// read-back verifies.
func TestGitHubEditErrorsWhenTheChangeIsNotThere(t *testing.T) {
	title, body := "new", "new body"
	const (
		plain   = `{"title":"old","body":"b","labels":[],"milestone":null}`
		labeled = `{"title":"old","body":"b","labels":[{"name":"p1"}],"milestone":null}`
		inMile  = `{"title":"old","body":"b","labels":[],"milestone":{"title":"M1"}}`
	)
	cases := []struct {
		name  string
		issue string
		edit  IssueEdit
		want  string
	}{
		{"title", plain, IssueEdit{Title: &title}, "title"},
		{"body", plain, IssueEdit{Body: &body}, "body"},
		{"added label", plain, IssueEdit{AddLabels: []string{"extra"}}, "label extra"},
		{"removed label", labeled, IssueEdit{RemoveLabels: []string{"p1"}}, "label p1 still set"},
		{"milestone", plain, IssueEdit{Milestone: "M1"}, "milestone"},
		{"removed milestone", inMile, IssueEdit{RemoveMilestone: true}, "milestone still set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &editRig{apply: false, issue: tc.issue}
			err := r.adapter(t).Edit(context.Background(), NumKey(7), tc.edit)
			var e *Error
			if !errors.As(err, &e) || !strings.Contains(e.Message, "#7") || !strings.Contains(e.Message, "without: "+tc.want) {
				t.Fatalf("got %v, want a read-back error naming %q", err, tc.want)
			}
		})
	}
}

// TestGitHubEditEmptyPatchIsANoOp: an edit that asks for nothing makes no call,
// neither a write nor a read-back.
func TestGitHubEditEmptyPatchIsANoOp(t *testing.T) {
	r := &editRig{apply: false, issue: `{}`}
	if err := r.adapter(t).Edit(context.Background(), NumKey(7), IssueEdit{}); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if len(r.calls) != 0 {
		t.Fatalf("an empty edit must call nothing: %v", r.calls)
	}
}

// TestGitHubEditLabelReadBackIgnoresCase: GitHub label names are
// case-insensitive, so a label that landed as "Bug" satisfies a request for
// "bug", and a removed "P1" is gone even when asked as "p1".
func TestGitHubEditLabelReadBackIgnoresCase(t *testing.T) {
	r := &editRig{apply: true,
		issue:  `{"title":"t","body":"b","labels":[{"name":"P1"}],"milestone":null}`,
		patchd: `{"title":"t","body":"b","labels":[{"name":"Bug"}],"milestone":null}`}
	err := r.adapter(t).Edit(context.Background(), NumKey(7), IssueEdit{AddLabels: []string{"bug"}, RemoveLabels: []string{"p1"}})
	if err != nil {
		t.Fatalf("got %v, want the read-back to match case-insensitively", err)
	}
}

func TestGitHubEditUnknownMilestoneIsNotFound(t *testing.T) {
	r := &editRig{issue: `{}`}
	err := r.adapter(t).Edit(context.Background(), NumKey(7), IssueEdit{Milestone: "nope"})
	var e *Error
	if !errors.As(err, &e) || e.Kind != KindNotFound {
		t.Fatalf("got %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("a missing milestone must change nothing: %v", r.calls)
	}
}
