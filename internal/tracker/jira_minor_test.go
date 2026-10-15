package tracker

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker/jiratest"
)

func TestIssueKeyProject(t *testing.T) {
	for k, want := range map[IssueKey]string{"PROJ-123": "PROJ", "A_B2-7": "A_B2", "603": "", "": "", "proj-1": "", "PROJ-": ""} {
		if got := k.Project(); got != want {
			t.Errorf("%q.Project() = %q, want %q", k, got, want)
		}
	}
}

func TestJiraCommentURLChecksTheKey(t *testing.T) {
	j, _ := fakeJira(t, jiratest.Options{})
	if _, err := j.CommentURL(context.Background(), "x/../../admin", "1"); !IsKind(err, KindNotFound) {
		t.Errorf("a bad key reached the URL: %v", err)
	}
	if u, err := j.CommentURL(context.Background(), "proj-12", "100 1"); err != nil || !strings.HasSuffix(u, "/browse/PROJ-12?focusedCommentId=100+1") {
		t.Errorf("CommentURL = %q, %v", u, err)
	}
}

func TestChunksEndOnInvalidUTF8(t *testing.T) {
	s := strings.Repeat("\x80", 2*propChunk+5) // continuation bytes only: no rune start
	parts := chunks(s)
	if strings.Join(parts, "") != s || len(parts) != 3 {
		t.Fatalf("%d parts", len(parts))
	}
}

// Close moves the issue first: a retry after a failed transition must not
// post its comment twice.
func TestJiraCloseTransitionsBeforeCommenting(t *testing.T) {
	ctx := context.Background()
	j, s := fakeJira(t, jiratest.Options{})
	k, err := j.Create(ctx, "t", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Close(ctx, k, "not_planned", "bye"); err != nil {
		t.Fatal(err)
	}
	var writes []string
	for _, r := range s.Requests() {
		if !strings.HasPrefix(r, "GET ") {
			writes = append(writes, strings.SplitN(r, "?", 2)[0])
		}
	}
	tr := slices.IndexFunc(writes, func(r string) bool { return strings.HasSuffix(r, "/transitions") })
	cm := slices.IndexFunc(writes, func(r string) bool { return strings.HasSuffix(r, "/comment") })
	if tr < 0 || cm < 0 || tr > cm {
		t.Fatalf("writes %v: the transition must come before the comment", writes)
	}
}

func TestREST403HintNamesPermission(t *testing.T) {
	if err := restError("GET", "/issue/PROJ-1", 403, nil); !strings.Contains(err.Message, "permission on this issue") {
		t.Errorf("403: %s", err.Message)
	}
}
