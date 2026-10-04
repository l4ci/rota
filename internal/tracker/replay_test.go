package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The fixtures in testdata/ are the retired M07 Python adapters recorded
// against the offline fake gh/glab: every CLI call each step made and what the
// adapter returned. They are frozen; the recorder is gone, so a fixture changes
// only by reviewed hand edit. Replaying them proves the Go adapters make
// the same calls, in the same order, and return the same values, apart from
// two deliberate fixes the review of #90 asked for (see divergeArgv and
// divergeResult).

type recCall struct {
	Name   string   `json:"name"`
	Argv   []string `json:"argv"`
	Stdin  string   `json:"stdin"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr"`
	Code   int      `json:"code"`
}

type recStep struct {
	Op     string                     `json:"op"`
	Args   map[string]json.RawMessage `json:"args"`
	Calls  []recCall                  `json:"calls"`
	Result json.RawMessage            `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type recording struct {
	Provider string    `json:"provider"`
	Steps    []recStep `json:"steps"`
}

func TestReplayRecordedFixtures(t *testing.T) {
	for _, p := range []string{"github", "gitlab"} {
		t.Run(p, func(t *testing.T) { replay(t, filepath.Join("testdata", p+".json")) })
	}
}

func replay(t *testing.T, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec recording
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	var calls []recCall
	var step string
	var op string
	x := func(_ context.Context, dir, name string, args []string, stdin []byte) ([]byte, []byte, int, error) {
		if len(calls) == 0 {
			t.Fatalf("%s: unexpected call %s %q", step, name, args)
		}
		c := calls[0]
		calls = calls[1:]
		want := divergeArgv(rec.Provider, op, c.Argv)
		if name != c.Name || !reflect.DeepEqual(args, want) {
			t.Fatalf("%s: call\n got %s %q\nwant %s %q", step, name, args, c.Name, want)
		}
		if string(stdin) != c.Stdin {
			t.Fatalf("%s: stdin %q, want %q", step, stdin, c.Stdin)
		}
		return []byte(c.Stdout), []byte(c.Stderr), c.Code, nil
	}
	found := func(string) (string, error) { return "/fake", nil }
	ctx := context.Background()
	a, err := New(ctx, Settings{Provider: rec.Provider, NotPlannedLabel: "not-planned"}, "", "",
		WithExec(x, found), WithSleep(func(time.Duration) {}))
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range rec.Steps {
		step = fmt.Sprintf("step %d %s %s", i, s.Op, s.Args)
		calls, op = s.Calls, s.Op
		got, err := dispatch(ctx, a, s.Op, s.Args)
		if len(calls) != 0 {
			t.Fatalf("%s: %d recorded calls not made, next %q", step, len(calls), calls[0].Argv)
		}
		if s.Error != nil {
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("%s: want error %d %q, got %v (result %v)", step, s.Error.Code, s.Error.Message, err, got)
			}
			want := strings.TrimPrefix(s.Error.Message, "error: rota tracker call: ")
			if e.Code != s.Error.Code || e.Message != want {
				t.Fatalf("%s: error [%d] %q, want [%d] %q", step, e.Code, e.Message, s.Error.Code, want)
			}
			// What the Python backend sniffed with _NOT_FOUND_RE is a kind now. A
			// label missing under autoCreateLabel off is also not found (the
			// #106 contract), though Python's message doesn't say so.
			notFound := pyNotFound.MatchString(s.Error.Message) || reMissingLabel.MatchString(s.Error.Message)
			if notFound != (e.Kind == KindNotFound) {
				t.Fatalf("%s: kind %d for %q", step, e.Kind, s.Error.Message)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		gotJSON, _ := json.Marshal(got)
		var g, w any
		_ = json.Unmarshal(gotJSON, &g)
		_ = json.Unmarshal(s.Result, &w)
		w = divergeResult(rec.Provider, s.Op, w)
		if !reflect.DeepEqual(g, w) {
			t.Fatalf("%s: result\n got %s\nwant %s", step, gotJSON, s.Result)
		}
	}
}

// dispatch calls the Go method for a recorded Python call and returns its
// result in the Python adapter's shape.
func dispatch(ctx context.Context, a Adapter, op string, args map[string]json.RawMessage) (any, error) {
	in := func(k string) int {
		var n int
		_ = json.Unmarshal(args[k], &n)
		return n
	}
	s := func(k string) string {
		var v string
		_ = json.Unmarshal(args[k], &v)
		return v
	}
	ptr := func(k string) *string {
		if _, ok := args[k]; !ok {
			return nil
		}
		v := s(k)
		return &v
	}
	list := func(k string) []string {
		var v []string
		_ = json.Unmarshal(args[k], &v)
		return v
	}
	flag := func(k string, def bool) bool {
		v := def
		if raw, ok := args[k]; ok {
			_ = json.Unmarshal(raw, &v)
		}
		return v
	}
	raw := func(k string) string { return strings.Trim(string(args[k]), `"`) }

	switch op {
	case "call":
		c := a.(interface{ cliOf() *CLI }).cliOf()
		r, err := c.Run(ctx, list("args"), strings.NewReader(s("stdin")))
		if err != nil {
			e := err.(*Error)
			return map[string]any{"stdout": "", "stderr": "error: rota tracker call: " + e.Message + "\n", "code": e.Code}, nil
		}
		return map[string]any{"stdout": string(r.Stdout), "stderr": string(r.Stderr), "code": r.ExitCode}, nil
	case "closed_numbers":
		return nonNil(a.ClosedNumbers(s("body"))), nil
	case "create":
		return a.Create(ctx, s("title"), s("body"), list("labels"), s("milestone"))
	case "get":
		is, err := a.Get(ctx, in("number"), flag("comments", false))
		return pyIssue(is), err
	case "list":
		return pyIssues(a.List(ctx, ListFilter{State: s("state"), Labels: list("labels"), Milestone: s("milestone")}))
	case "issues_in_milestone":
		return pyIssues(a.IssuesInMilestone(ctx, s("title"), s("state")))
	case "edit":
		return nil, a.Edit(ctx, in("number"), IssueEdit{Title: ptr("title"), Body: ptr("body"), AddLabels: list("add_labels"),
			RemoveLabels: list("remove_labels"), Milestone: s("milestone"), RemoveMilestone: flag("remove_milestone", false)})
	case "ensure_labels":
		return nil, a.EnsureLabels(ctx, list("names"), flag("auto_create", true))
	case "add_labels":
		return nil, a.AddLabels(ctx, in("number"), list("labels"), flag("auto_create", true))
	case "remove_labels":
		return nil, a.RemoveLabels(ctx, in("number"), list("labels"))
	case "close":
		return nil, a.Close(ctx, in("number"), s("reason"), s("comment"))
	case "reopen":
		return nil, a.Reopen(ctx, in("number"))
	case "assign_self":
		return nil, a.AssignSelf(ctx, in("number"))
	case "comments":
		cs, err := a.Comments(ctx, in("number"))
		return pyComments(cs), err
	case "add_comment":
		id, err := a.AddComment(ctx, in("number"), s("body"))
		return pyID(id), err
	case "edit_comment":
		return nil, a.EditComment(ctx, in("number"), raw("comment_id"), s("body"))
	case "delete_comment":
		return nil, a.DeleteComment(ctx, in("number"), raw("comment_id"))
	case "find_milestone":
		t, ok, err := a.FindMilestone(ctx, s("hv_id"))
		if !ok {
			return nil, err
		}
		return t, err
	case "milestones":
		ms, err := a.Milestones(ctx, s("state"))
		out := []any{}
		for _, m := range ms {
			out = append(out, map[string]any{"number": m.Number, "title": m.Title, "description": m.Description, "state": m.State})
		}
		return out, err
	case "create_milestone":
		return a.CreateMilestone(ctx, s("title"), s("description"))
	case "edit_milestone":
		return nil, a.EditMilestone(ctx, in("number"), MilestoneEdit{Title: ptr("title"), Description: ptr("description"), State: ptr("state")})
	case "open_prs":
		return pyPRs(a.OpenPRs(ctx))
	case "prs_closing":
		return pyPRs(a.PRsClosing(ctx, in("number")))
	case "pr_checkout":
		return nil, a.PRCheckout(ctx, in("pr"))
	case "pr_merge":
		return a.PRMerge(ctx, in("pr"))
	case "pr_comment":
		return nil, a.PRComment(ctx, in("pr"), s("body"))
	case "pr_state":
		return a.PRState(ctx, in("pr"))
	}
	return nil, fmt.Errorf("no dispatch for op %q", op)
}

func (b *base) cliOf() *CLI { return b.cli }

// reMissingLabel is the ensure_labels refusal when autoCreateLabel is off.
var reMissingLabel = regexp.MustCompile(`^label '.*' does not exist \(issues\.autoCreateLabel is off\)$`)

// pyNotFound is the Python backend's _NOT_FOUND_RE.
var pyNotFound = regexp.MustCompile(`(?i)not found|could not resolve|404`)

// divergeArgv is the argv the Go port sends where it fixes the Python one:
// glab merges with auto-merge off, so a pipeline can't turn the merge into a
// scheduled one that merges nothing.
func divergeArgv(provider, op string, argv []string) []string {
	if provider == "gitlab" && op == "pr_merge" && len(argv) > 1 && argv[0] == "mr" && argv[1] == "merge" {
		return append(append([]string(nil), argv...), "--auto-merge=false")
	}
	return argv
}

// divergeResult is the result the Go port returns where it fixes the Python
// one: gh `issue view` comment ids are GraphQL node ids, which the comment
// API rejects, so Go reports the REST id from the comment url. The fake
// spells a node id IC_fake<rest id>.
func divergeResult(provider, op string, w any) any {
	m, ok := w.(map[string]any)
	if provider != "github" || op != "get" || !ok {
		return w
	}
	cs, _ := m["comments"].([]any)
	for _, c := range cs {
		cm := c.(map[string]any)
		id := strings.TrimPrefix(cm["id"].(string), "IC_fake")
		n, _ := strconv.Atoi(id)
		cm["id"] = float64(n)
	}
	return m
}

func nonNil(ns []int) []int {
	if ns == nil {
		return []int{}
	}
	return ns
}

func none(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func pyID(id string) any {
	if n, err := strconv.Atoi(id); err == nil {
		return n
	}
	return id
}

func pyIssue(is Issue) map[string]any {
	m := map[string]any{"number": is.Number, "title": is.Title, "body": is.Body, "labels": is.Labels,
		"milestone": none(is.Milestone), "state": is.State, "state_reason": none(is.StateReason),
		"closed_at": none(is.ClosedAt), "url": is.URL, "assignees": is.Assignees}
	if is.Comments != nil {
		m["comments"] = pyComments(is.Comments)
	}
	return m
}

func pyIssues(list []Issue, err error) (any, error) {
	out := []any{}
	for _, is := range list {
		out = append(out, pyIssue(is))
	}
	return out, err
}

func pyComments(cs []Comment) []any {
	out := []any{}
	for _, c := range cs {
		out = append(out, map[string]any{"id": pyID(c.ID), "body": c.Body, "author": c.Author})
	}
	return out
}

func pyPRs(prs []PR, err error) (any, error) {
	out := []any{}
	for _, p := range prs {
		out = append(out, map[string]any{"number": p.Number, "title": p.Title, "branch": p.Branch, "url": p.URL, "body": p.Body})
	}
	return out, err
}
