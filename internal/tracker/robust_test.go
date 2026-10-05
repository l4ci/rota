package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// scripted is an executor that answers by argv; it is safe for concurrent use.
type scripted struct {
	mu     sync.Mutex
	calls  []string
	answer func(name string, args []string) (string, string, int)
}

func (s *scripted) exec(_ context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
	line := name + " " + strings.Join(args, " ")
	s.mu.Lock()
	s.calls = append(s.calls, line)
	s.mu.Unlock()
	out, errs, code := s.answer(name, args)
	return []byte(out), []byte(errs), code, nil
}

func newAdapter(t *testing.T, provider string, s *scripted) Adapter {
	t.Helper()
	a, err := New(context.Background(), Settings{Provider: provider, NotPlannedLabel: "not-planned"}, "", "/repo",
		WithExec(s.exec, func(string) (string, error) { return "/fake", nil }), WithSleep(func(time.Duration) {}))
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestKindExit(t *testing.T) {
	for k, want := range map[Kind]int{KindFailed: 5, KindUnavailable: 5, KindRateLimited: 6, KindNotFound: 3, KindInternal: 70} {
		if got := k.Exit(); got != want {
			t.Errorf("kind %d: exit %d, want %d", k, got, want)
		}
	}
}

// TestNotFound feeds each adapter the messages the real CLIs print for a
// missing object, and a plain failure that must stay KindFailed.
func TestNotFound(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		provider, stderr string
		kind             Kind
	}{
		{"github", "GraphQL: Could not resolve to an issue or pull request with the number of 99. (repository.issue)", KindNotFound},
		{"github", "HTTP 404: Not Found (https://api.github.com/repos/o/r/issues/comments/9)", KindNotFound},
		{"github", "no pull requests found for branch \"x\"", KindNotFound},
		{"github", "HTTP 422: Validation Failed", KindFailed},
		{"gitlab", "ERROR: 404 Not Found", KindNotFound},
		{"gitlab", "404 Not found", KindNotFound},
		{"gitlab", "ERROR: 500 Internal Server Error", KindFailed},
		{"gitlab", "ERROR: 404 Project Not Found", KindFailed},
		{"github", "GraphQL: Could not resolve to a Repository with the name 'o/r'. (repository)", KindFailed},
	}
	for _, c := range cases {
		a := newAdapter(t, c.provider, &scripted{answer: func(string, []string) (string, string, int) { return "", c.stderr, 1 }})
		for name, call := range map[string]func() error{
			"Get":           func() error { _, err := a.Get(ctx, 99, false); return err },
			"EditComment":   func() error { return a.EditComment(ctx, 1, "9", "x") },
			"PRState":       func() error { _, err := a.PRState(ctx, 9); return err },
			"EditMilestone": func() error { return a.EditMilestone(ctx, 9, MilestoneEdit{}) },
		} {
			err := call()
			var e *Error
			if !errors.As(err, &e) || e.Kind != c.kind || e.Code != 1 {
				t.Errorf("%s %s %q: %#v, want kind %d", c.provider, name, c.stderr, err, c.kind)
			}
		}
	}
}

func TestAttemptTimeout(t *testing.T) {
	block := func(ctx context.Context, _, _ string, _ []string, _ []byte) ([]byte, []byte, int, error) {
		<-ctx.Done()
		return nil, nil, 0, ctx.Err()
	}
	c := &CLI{Provider: "github", Timeout: 20 * time.Millisecond, Exec: block,
		LookPath: func(string) (string, error) { return "/fake", nil }}
	start := time.Now()
	_, err := c.Run(context.Background(), []string{"issue", "list"}, nil)
	if !IsKind(err, KindUnavailable) || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout not honoured")
	}
	// A cancelled caller is an internal stop, not a forge failure.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Run(ctx, []string{"issue", "list"}, nil); !IsKind(err, KindInternal) {
		t.Fatalf("cancelled: %v", err)
	}
}

// TestDefaultExecTimeoutKillsProcess runs a real process (a shell, never gh)
// through the default executor.
func TestDefaultExecTimeoutKillsProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, _, err := osExec(ctx, "", "sh", []string{"-c", "sleep 30"}, nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 10*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestRetryWaitIsCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &CLI{Provider: "github", RetryWait: time.Hour,
		LookPath: func(string) (string, error) { return "/fake", nil },
		Exec: func(context.Context, string, string, []string, []byte) ([]byte, []byte, int, error) {
			cancel()
			return nil, []byte("API rate limit exceeded"), 1, nil
		}}
	done := make(chan error, 1)
	go func() { _, err := c.Run(ctx, []string{"issue", "list"}, nil); done <- err }()
	select {
	case err := <-done:
		if !IsKind(err, KindInternal) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the rate-limit wait ignored ctx")
	}
}

func TestExecStartFailureIsInternal(t *testing.T) {
	c := &CLI{Provider: "github", LookPath: func(string) (string, error) { return "/fake", nil },
		Exec: func(context.Context, string, string, []string, []byte) ([]byte, []byte, int, error) {
			return nil, nil, 0, &os.PathError{Op: "chdir", Path: "/nope", Err: os.ErrNotExist}
		}}
	_, err := c.Run(context.Background(), []string{"issue", "list"}, nil)
	if !IsKind(err, KindInternal) || KindInternal.Exit() != 70 {
		t.Fatalf("got %v", err)
	}
	// The real executor with a missing directory takes the same path.
	c.Exec = nil
	c.Dir = "/nonexistent/dir/for/tracker/test"
	if _, err := c.Run(context.Background(), []string{"issue", "list"}, nil); !IsKind(err, KindInternal) {
		t.Fatalf("bad dir: %v", err)
	}
}

func rowsJSON(from, n int, key string) string {
	rows := make([]map[string]any, n)
	for i := range rows {
		body := fmt.Sprintf("Closes #%d", from+i)
		rows[i] = map[string]any{key: from + i, "body": body, "description": body}
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

func TestListsPageUntilShort(t *testing.T) {
	ctx := context.Background()
	t.Run("github grows the limit", func(t *testing.T) {
		s := &scripted{answer: func(_ string, args []string) (string, string, int) {
			limit := args[len(args)-1]
			switch limit {
			case "1000":
				return rowsJSON(1, 1000, "number"), "", 0
			case "2000":
				return rowsJSON(1, 1500, "number"), "", 0
			}
			return "", "unexpected limit " + limit, 1
		}}
		a := newAdapter(t, "github", s)
		is, err := a.List(ctx, ListFilter{})
		if err != nil || len(is) != 1500 || is[1499].Number != 1500 {
			t.Fatalf("%d issues, %v", len(is), err)
		}
		if len(s.calls) != 2 || !strings.HasSuffix(s.calls[1], "--limit 2000") {
			t.Fatalf("calls %q", s.calls)
		}
		prs, err := a.PRsClosing(ctx, 1400)
		if err != nil || len(prs) != 1 || prs[0].Number != 1400 {
			t.Fatalf("PRsClosing past the first limit: %+v %v", prs, err)
		}
	})
	t.Run("gitlab pages", func(t *testing.T) {
		s := &scripted{answer: func(_ string, args []string) (string, string, int) {
			line := strings.Join(args, " ")
			switch {
			case strings.HasSuffix(line, "--per-page 100"):
				return rowsJSON(1, 100, "iid"), "", 0
			case strings.HasSuffix(line, "--per-page 100 --page 2"):
				return rowsJSON(101, 100, "iid"), "", 0
			case strings.HasSuffix(line, "--per-page 100 --page 3"):
				return rowsJSON(201, 7, "iid"), "", 0
			}
			return "", "unexpected " + line, 1
		}}
		a := newAdapter(t, "gitlab", s)
		is, err := a.List(ctx, ListFilter{State: "all"})
		if err != nil || len(is) != 207 || is[206].Number != 207 {
			t.Fatalf("%d issues, %v", len(is), err)
		}
		prs, err := a.PRsClosing(ctx, 150)
		if err != nil || len(prs) != 1 || prs[0].Number != 150 {
			t.Fatalf("PRsClosing on page 2: %+v %v", prs, err)
		}
		if len(s.calls) != 6 {
			t.Fatalf("calls %q", s.calls)
		}
	})
}

func TestGitHubCommentIDsAreRESTOnEveryPath(t *testing.T) {
	ctx := context.Background()
	view := `{"number":1,"comments":[{"id":"IC_kwDOSDMYAc8AAAABYrOCaA","body":"x","author":{"login":"a"},` +
		`"url":"https://github.com/o/r/issues/1#issuecomment-5950898792"}]}`
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		if args[0] == "api" {
			return `[{"id":5950898792,"body":"x","user":{"login":"a"}}]`, "", 0
		}
		return view, "", 0
	}}
	a := newAdapter(t, "github", s)
	is, err := a.Get(ctx, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := a.Comments(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if is.Comments[0].ID != "5950898792" || cs[0].ID != is.Comments[0].ID {
		t.Fatalf("view id %q, api id %q", is.Comments[0].ID, cs[0].ID)
	}
	if err := a.DeleteComment(ctx, 1, is.Comments[0].ID); err != nil ||
		s.calls[len(s.calls)-1] != "gh api -X DELETE repos/{owner}/{repo}/issues/comments/5950898792" {
		t.Fatalf("delete ran %q, %v", s.calls[len(s.calls)-1], err)
	}
	// A comment without a url can't be given a REST id: that is an error,
	// never a node id slipped through.
	view = `{"number":1,"comments":[{"id":"IC_x","body":"x"}]}`
	if _, err := a.Get(ctx, 1, true); !IsKind(err, KindFailed) {
		t.Fatalf("no url: %v", err)
	}
}

func TestGitLabAssignSelfConcurrent(t *testing.T) {
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		if args[0] == "api" {
			return `{"username":"me"}`, "", 0
		}
		return "", "", 0
	}}
	a := newAdapter(t, "gitlab", s)
	var wg sync.WaitGroup
	for i := 1; i <= 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := a.AssignSelf(context.Background(), n); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	users := 0
	for _, c := range s.calls {
		if strings.HasPrefix(c, "glab api user") {
			users++
		}
	}
	if users != 1 || len(s.calls) != 9 {
		t.Fatalf("%d user lookups in %q", users, s.calls)
	}
}

func TestGitLabPRMerge(t *testing.T) {
	ctx := context.Background()
	const head = "aaaa1111"
	cases := []struct {
		name     string
		view     string
		ancestor int // exit of git merge-base --is-ancestor; -1: not called
		sha      string
		errHas   string
	}{
		{"merge commit", `{"state":"merged","merge_commit_sha":"mmmm"}`, -1, "mmmm", ""},
		{"squash", `{"state":"merged","merge_commit_sha":null,"squash_commit_sha":"ssss"}`, -1, "ssss", ""},
		{"fast-forward on base", `{"state":"merged","merge_commit_sha":null,"sha":"` + head + `","target_branch":"main"}`, 0, head, ""},
		{"fast-forward elsewhere", `{"state":"merged","sha":"` + head + `","target_branch":"main"}`, 1, "", "not on origin/main"},
		{"merge-base broke", `{"state":"merged","sha":"` + head + `","target_branch":"main"}`, 128, "", "failed"},
		{"auto-merge scheduled", `{"state":"opened","merge_commit_sha":null,"sha":"` + head + `"}`, -1, "", "is opened after the merge call"},
	}
	for _, c := range cases {
		s := &scripted{answer: func(name string, args []string) (string, string, int) {
			line := strings.Join(args, " ")
			switch {
			case name == "glab" && line == "mr merge 4 --yes --remove-source-branch --auto-merge=false":
				return "", "", 0
			case name == "glab" && line == "mr view 4 --output json":
				return c.view, "", 0
			case name == "git" && line == "fetch -q origin":
				return "", "", 0
			case name == "git" && line == "merge-base --is-ancestor "+head+" origin/main":
				return "", "fatal: bad object", c.ancestor
			}
			return "", "unexpected " + name + " " + line, 2
		}}
		sha, err := newAdapter(t, "gitlab", s).PRMerge(ctx, 4, MergeOpts{DeleteBranch: true})
		calledGit := false
		for _, l := range s.calls {
			calledGit = calledGit || strings.HasPrefix(l, "git merge-base")
		}
		if calledGit != (c.ancestor >= 0) {
			t.Errorf("%s: merge-base called %v in %q", c.name, calledGit, s.calls)
		}
		if c.errHas == "" {
			if err != nil || sha != c.sha {
				t.Errorf("%s: %q, %v; want %q", c.name, sha, err, c.sha)
			}
			continue
		}
		if !IsKind(err, KindFailed) || !strings.Contains(err.Error(), c.errHas) {
			t.Errorf("%s: %v; want KindFailed with %q", c.name, err, c.errHas)
		}
	}
}

func TestGitLabGetDropsSystemNotesLikeComments(t *testing.T) {
	ctx := context.Background()
	notes := `[{"id":7,"body":"added label","system":true},{"id":8,"body":"hi","author":{"username":"u"}}]`
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		if args[0] == "api" {
			return notes, "", 0
		}
		return `{"iid":1,"notes":` + notes + `}`, "", 0
	}}
	a := newAdapter(t, "gitlab", s)
	is, err := a.Get(ctx, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := a.Comments(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(is.Comments) != 1 || is.Comments[0] != cs[0] || len(cs) != 1 {
		t.Fatalf("Get %+v, Comments %+v", is.Comments, cs)
	}
}

func TestGitLabMergeGitCallsTimeOut(t *testing.T) {
	a, err := New(context.Background(), Settings{Provider: "gitlab"}, "", "/repo",
		WithTimeout(20*time.Millisecond),
		WithExec(func(ctx context.Context, _, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
			switch {
			case name == "git":
				<-ctx.Done()
				return nil, nil, 0, ctx.Err()
			case args[1] == "view":
				return []byte(`{"state":"merged","sha":"aaaa","target_branch":"main"}`), nil, 0, nil
			}
			return nil, nil, 0, nil
		}, func(string) (string, error) { return "/fake", nil }))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := a.PRMerge(context.Background(), 4, MergeOpts{DeleteBranch: true}); done <- err }()
	select {
	case err := <-done:
		if !IsKind(err, KindFailed) || !strings.Contains(err.Error(), "git fetch origin failed") {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("git fetch ran without the per-attempt timeout")
	}
}

// TestMissingLabelIsNotFound: with autoCreateLabel off, a label the tracker
// lacks is exit 3 (contract #106), through EnsureLabels and AddLabels; with
// it on, the label is created. glab creates labels on first use, so GitLab
// never refuses.
func TestMissingLabelIsNotFound(t *testing.T) {
	ctx := context.Background()
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		if args[0] == "label" && args[1] == "list" {
			return `[{"name":"bug"}]`, "", 0
		}
		return "", "", 0
	}}
	gh := newAdapter(t, "github", s)
	for name, err := range map[string]error{
		"EnsureLabels": gh.EnsureLabels(ctx, []string{"bug", "nope"}, false),
		"AddLabels":    gh.AddLabels(ctx, 1, []string{"nope"}, false),
	} {
		var e *Error
		if !errors.As(err, &e) || e.Kind != KindNotFound || e.Kind.Exit() != 3 || !strings.Contains(e.Message, "label 'nope' does not exist") {
			t.Errorf("%s: %#v", name, err)
		}
	}
	for _, c := range s.calls {
		if strings.Contains(c, "label create") || strings.Contains(c, "issue edit") {
			t.Fatalf("a refused label must change nothing: %q", s.calls)
		}
	}
	if err := gh.EnsureLabels(ctx, []string{"nope"}, true); err != nil || s.calls[len(s.calls)-1] != "gh label create nope --force" {
		t.Fatalf("autoCreate on: %v, %q", err, s.calls)
	}
	gl := newAdapter(t, "gitlab", &scripted{answer: func(string, []string) (string, string, int) { return "", "", 0 }})
	if err := gl.AddLabels(ctx, 1, []string{"nope"}, false); err != nil {
		t.Fatalf("gitlab: %v", err)
	}
}

// TestGitHubOldGhNoStateReason: gh 2.45 rejects the stateReason field. List
// and Get retry without it, the reason reads as unknown, and later calls skip
// the doomed first try.
func TestGitHubOldGhNoStateReason(t *testing.T) {
	const row = `{"number":3,"title":"t","body":"","labels":[],"milestone":null,"state":"CLOSED","closedAt":"2026-01-01T00:00:00Z","url":"u","assignees":[]}`
	s := &scripted{answer: func(_ string, args []string) (string, string, int) {
		if strings.Contains(strings.Join(args, " "), "stateReason") {
			return "", "Unknown JSON field: \"stateReason\"\nAvailable fields:\n  assignees\n", 1
		}
		if args[1] == "list" {
			return "[" + row + "]", "", 0
		}
		return row, "", 0
	}}
	a := newAdapter(t, "github", s)
	ctx := context.Background()
	got, err := a.List(ctx, ListFilter{State: "closed"})
	if err != nil || len(got) != 1 || got[0].State != "closed" || got[0].StateReason != "" {
		t.Fatalf("list: %+v, %v", got, err)
	}
	if len(s.calls) != 2 {
		t.Fatalf("list calls %q, want a rejected try and a retry", s.calls)
	}
	is, err := a.Get(ctx, 3, false)
	if err != nil || is.Number != 3 || is.State != "closed" {
		t.Fatalf("get: %+v, %v", is, err)
	}
	if len(s.calls) != 3 || strings.Contains(s.calls[2], "stateReason") {
		t.Fatalf("get should skip the rejected field, calls %q", s.calls)
	}
}
