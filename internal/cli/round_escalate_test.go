package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/host"
	"github.com/l4ci/rota/internal/tracker"
)

// fakeThread is a scripted gh/glab for one thread's comments. It answers the
// REST calls the tracker adapters make and records every call.
type fakeThread struct {
	comments []map[string]any
	next     int
	calls    []string
	failRead error // returned by the list call
	notFound bool  // every call says the object does not exist
}

func (f *fakeThread) add(body string) string {
	f.next++
	f.comments = append(f.comments, map[string]any{"id": f.next, "body": body})
	return strconv.Itoa(f.next)
}

func (f *fakeThread) remove(id string) {
	for i, c := range f.comments {
		if fmt.Sprint(c["id"]) == id {
			f.comments = append(f.comments[:i], f.comments[i+1:]...)
			return
		}
	}
}

func (f *fakeThread) exec(_ context.Context, _ string, name string, args []string, _ []byte) ([]byte, []byte, int, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if f.notFound {
		return nil, []byte("HTTP 404: Not Found"), 1, nil
	}
	post := strings.Contains(line, "-X POST")
	switch {
	case post:
		var body string
		for _, a := range args {
			if strings.HasPrefix(a, "body=") {
				body = strings.TrimPrefix(a, "body=")
			}
		}
		id := f.add(body)
		return []byte(`{"id":` + id + `}`), nil, 0, nil
	case strings.Contains(line, "/comments/") && name == "gh":
		id := strings.Fields(line[strings.LastIndex(line, "/")+1:])[0]
		return []byte(`{"id":` + id + `,"html_url":"https://github.com/o/r/issues/7#issuecomment-` + id + `"}`), nil, 0, nil
	case name == "glab" && strings.Contains(line, " view "):
		return []byte(`{"web_url":"https://gitlab.com/o/r/-/merge_requests/7"}`), nil, 0, nil
	case strings.Contains(line, "/comments") || strings.Contains(line, "/notes"):
		if f.failRead != nil {
			return nil, []byte(f.failRead.Error()), 1, nil
		}
		rows := []map[string]any{}
		for _, c := range f.comments {
			r := map[string]any{"id": c["id"], "body": c["body"], "user": map[string]any{"login": "maint"}, "author": map[string]any{"username": "maint"}, "system": false}
			rows = append(rows, r)
		}
		b, _ := json.Marshal(rows)
		return b, nil, 0, nil
	}
	return nil, []byte("unexpected call: " + line), 2, nil
}

func (f *fakeThread) reads() int {
	n := 0
	for _, c := range f.calls {
		if !strings.Contains(c, "-X POST") && (strings.Contains(c, "/comments") || strings.Contains(c, "/notes")) && !strings.Contains(c, "/comments/") {
			n++
		}
	}
	return n
}

func useThread(t *testing.T, f *fakeThread) *Deps {
	t.Helper()
	deps := testDeps()
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(f.exec, func(n string) (string, error) { return "/fake/" + n, nil })}
	return deps
}

// escHost is a herdr host that only records notifications.
type escHost struct {
	cliHost
	require error
	notes   []string
}

func (h *escHost) Require() error { return h.require }
func (h *escHost) Notify(_ context.Context, title, body string) {
	h.notes = append(h.notes, title+"|"+body)
}

// useEscalation sets the escalation env on deps: a clock the test moves, a fake
// host, and HERDR_ENV as given.
func useEscalation(deps *Deps, h *escHost, herdrEnv string, clock *time.Time) {
	deps.EscalationEnv = func() escalation.Env {
		return escalation.Env{
			Now:    func() time.Time { return *clock },
			Getenv: func(k string) string { return map[string]string{"HERDR_ENV": herdrEnv}[k] },
			Host:   func() host.Host { return h },
		}
	}
}

func escProject(t *testing.T, cfg string) string {
	t.Helper()
	dir := workerProject(t, cfg)
	if err := os.WriteFile(filepath.Join(dir, "body.md"), []byte("Which option?\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func registryDoc(t *testing.T, dir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".rota", "workers.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

const ghCfg = `{"issues":{"provider":"github"}}`

func TestEscalateSendRecordsAndPosts(t *testing.T) {
	dir := escProject(t, ghCfg)
	f := &fakeThread{}
	deps := useThread(t, f)
	h := &escHost{cliHost: cliHost{herdr: true}}
	clock := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	useEscalation(deps, h, "1", &clock)

	code, out, errOut := rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "Pick one", "--body-file", "body.md", "--timeout", "3600", "--json")
	d := data(t, out)
	if code != 0 {
		t.Fatalf("send: %d %s %s", code, out, errOut)
	}
	e := d["escalation"].(map[string]any)
	if e["id"] != "e1" || e["kind"] != "issue" || e["number"] != 7.0 || e["status"] != "pending" || e["notified"] != true ||
		e["sentAt"] != "2026-10-03T12:00:00Z" || e["deadline"] != "2026-10-03T13:00:00Z" || e["commentId"] != "1" || e["title"] != "Pick one" {
		t.Errorf("entry: %v", e)
	}
	if _, has := e["slot"]; has {
		t.Errorf("slot must be absent: %v", e)
	}
	if d["url"] != "https://github.com/o/r/issues/7#issuecomment-1" || d["notified"] != true || d["changed"] != true {
		t.Errorf("data: %v", d)
	}
	body := f.comments[0]["body"].(string)
	if !strings.HasPrefix(body, "**rota escalation e1**: Pick one\n") || !strings.Contains(body, "Which option?") ||
		!strings.Contains(body, "Answer in a new comment on this thread.") || strings.Contains(body, "m:") || !strings.HasSuffix(body, "<!-- rota:escalation e1 -->\n") {
		t.Errorf("comment body:\n%s", body)
	}
	if !strings.Contains(f.calls[0], "repos/{owner}/{repo}/issues/7/comments") {
		t.Errorf("issue thread must use the issue comments API: %q", f.calls[0])
	}
	if len(h.notes) != 1 || h.notes[0] != "rota escalation e1|Pick one (#7)" {
		t.Errorf("notifications: %v", h.notes)
	}
	list := registryDoc(t, dir)["escalations"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != "e1" {
		t.Errorf("registry: %v", list)
	}
}

func TestEscalateSendOnePerThread(t *testing.T) {
	dir := escProject(t, ghCfg)
	f := &fakeThread{}
	deps := useThread(t, f)
	h := &escHost{cliHost: cliHost{herdr: true}}
	clock := time.Now().UTC()
	useEscalation(deps, h, "1", &clock)
	if code, out, _ := rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "A", "--body-file", "body.md"); code != 0 {
		t.Fatalf("first send: %d %s", code, out)
	}
	posted := len(f.comments)
	code, out, _ := rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "B", "--body-file", "body.md", "--json")
	d := data(t, out)
	if code != 4 || d["pending"] != "e1" || d["changed"] != false {
		t.Fatalf("second send: %d %v", code, d)
	}
	if len(f.comments) != posted {
		t.Error("a refused send must not post")
	}
	// The same number as a PR is another thread; the id keeps counting.
	code, out, _ = rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--pr", "--title", "C", "--body-file", "body.md", "--json")
	if e := data(t, out)["escalation"].(map[string]any); code != 0 || e["id"] != "e2" || e["kind"] != "pr" {
		t.Fatalf("pr send: %d %s", code, out)
	}
	if !strings.Contains(f.calls[len(f.calls)-2], "repos/{owner}/{repo}/issues/7/comments") {
		t.Errorf("GitHub PR threads use the issue comments API: %v", f.calls)
	}
}

func TestEscalateSendUsageAndResolution(t *testing.T) {
	dir := escProject(t, ghCfg)
	f := &fakeThread{}
	deps := useThread(t, f)
	clock := time.Now().UTC()
	useEscalation(deps, &escHost{}, "", &clock)
	os.WriteFile(filepath.Join(dir, "empty.md"), []byte(" \n\n"), 0o644)
	for name, args := range map[string][]string{
		"no number":        {"--title", "t", "--body-file", "body.md"},
		"zero":             {"0", "--title", "t", "--body-file", "body.md"},
		"not a number":     {"x", "--title", "t", "--body-file", "body.md"},
		"no title":         {"7", "--body-file", "body.md"},
		"no body":          {"7", "--title", "t"},
		"empty body":       {"7", "--title", "t", "--body-file", "empty.md"},
		"missing body":     {"7", "--title", "t", "--body-file", "nope.md"},
		"negative timeout": {"7", "--title", "t", "--body-file", "body.md", "--timeout", "-1"},
		"bad timeout":      {"7", "--title", "t", "--body-file", "body.md", "--timeout", "soon"},
	} {
		if code, _, _ := rotaInWith(t, deps, dir, append([]string{"round", "escalate", "send"}, args...)...); code != 2 {
			t.Errorf("%s: exit %d, want 2", name, code)
		}
	}
	if len(f.calls) != 0 {
		t.Errorf("usage errors must not reach the forge: %v", f.calls)
	}
	if code, _, _ := rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--slot", "ghost", "--title", "t", "--body-file", "body.md"); code != 3 {
		t.Errorf("unknown slot: %d, want 3", code)
	}
	if len(f.calls) != 0 {
		t.Errorf("slot resolution comes before the forge: %v", f.calls)
	}
	bare := t.TempDir()
	if code, _, _ := rotaInWith(t, deps, bare, "round", "escalate", "send", "7", "--title", "t", "--body-file", filepath.Join(dir, "body.md")); code != 3 {
		t.Errorf("no project root: %d, want 3", code)
	}
	f.notFound = true
	if code, _, _ := rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "t", "--body-file", "body.md"); code != 3 {
		t.Errorf("forge has no such issue: %d, want 3", code)
	}
	if doc := registryDoc(t, dir); doc["escalations"] != nil {
		t.Errorf("a failed send records nothing: %v", doc)
	}
}

func TestEscalateSendForgeMissing(t *testing.T) {
	dir := escProject(t, ghCfg)
	deps := testDeps()
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(nil, func(string) (string, error) { return "", exec.ErrNotFound })}
	clock := time.Now().UTC()
	useEscalation(deps, &escHost{}, "", &clock)
	if code, _, _ := rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "t", "--body-file", "body.md"); code != 5 {
		t.Errorf("forge CLI missing: %d, want 5", code)
	}
	if code, _, _ := rotaInWith(t, deps, dir, "round", "escalate", "check"); code != 0 {
		t.Errorf("check with nothing pending needs no forge: %d, want 0", code)
	}
}

func TestEscalateSendNotifyRules(t *testing.T) {
	for _, c := range []struct {
		name, cfg, env string
		require        error
		notified       bool
	}{
		{"HERDR_ENV", ghCfg, "1", nil, true},
		{"work.dispatch herdr", `{"issues":{"provider":"github"},"work":{"dispatch":"herdr"}}`, "", nil, true},
		{"neither", ghCfg, "", nil, false},
		{"tmux dispatch", `{"issues":{"provider":"github"},"work":{"dispatch":"tmux"}}`, "", nil, false},
		{"herdr not installed", ghCfg, "1", errors.New("herdr is not installed"), false},
	} {
		dir := escProject(t, c.cfg)
		deps := useThread(t, &fakeThread{})
		h := &escHost{require: c.require}
		clock := time.Now().UTC()
		useEscalation(deps, h, c.env, &clock)
		code, out, errOut := rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "t", "--body-file", "body.md", "--json")
		d := data(t, out)
		if code != 0 || d["notified"] != c.notified || d["escalation"].(map[string]any)["notified"] != c.notified {
			t.Errorf("%s: %d %v", c.name, code, d)
		}
		if got := len(h.notes) == 1; got != c.notified {
			t.Errorf("%s: notes %v", c.name, h.notes)
		}
		if !c.notified && !strings.Contains(errOut, "no notification") {
			t.Errorf("%s: missing warning: %s", c.name, errOut)
		}
	}
}

func TestEscalateSendSlotAndGitLabPR(t *testing.T) {
	dir := workerProject(t, `{"issues":{"provider":"gitlab"}}`)
	os.WriteFile(filepath.Join(dir, "body.md"), []byte("q\n"), 0o644)
	if code, _, _ := rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main"); code != 0 {
		t.Fatal("pool init")
	}
	f := &fakeThread{}
	deps := useThread(t, f)
	clock := time.Now().UTC()
	useEscalation(deps, &escHost{}, "", &clock)
	code, out, _ := rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--pr", "--slot", "w1", "--title", "t", "--body-file", "body.md", "--json")
	d := data(t, out)
	e, _ := d["escalation"].(map[string]any)
	if code != 0 || e["slot"] != "w1" || d["url"] != "https://gitlab.com/o/r/-/merge_requests/7#note_1" {
		t.Fatalf("%d %v", code, d)
	}
	if !strings.Contains(f.calls[0], "projects/:id/merge_requests/7/notes") {
		t.Errorf("GitLab PR threads are MR notes: %q", f.calls[0])
	}
	// Pool state survives next to the escalations list.
	doc := registryDoc(t, dir)
	if len(doc["slots"].([]any)) != 1 || len(doc["escalations"].([]any)) != 1 {
		t.Errorf("registry: %v", doc)
	}
}

func TestEscalateCheckLifecycle(t *testing.T) {
	dir := escProject(t, ghCfg)
	f := &fakeThread{}
	deps := useThread(t, f)
	h := &escHost{}
	clock := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	useEscalation(deps, h, "", &clock)
	rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "A", "--body-file", "body.md", "--timeout", "60")
	f.add("<!-- rota:claim dana -->\nClaimed by dana")
	f.add("<!-- rota:escalation e9 -->\nnot an answer either")

	check := func(args ...string) (int, map[string]any, string) {
		code, out, errOut := rotaInWith(t, deps, dir, append([]string{"round", "escalate", "check", "--json"}, args...)...)
		return code, data(t, out), errOut
	}
	rowStatus := func(d map[string]any, i int) string {
		return d["escalations"].([]any)[i].(map[string]any)["status"].(string)
	}

	code, d, _ := check()
	if code != 0 || d["pending"] != 1.0 || d["answered"] != 0.0 || d["timedOut"] != 0.0 || d["changed"] != false || rowStatus(d, 0) != "pending" {
		t.Fatalf("no answer yet: %d %v", code, d)
	}

	// Past the deadline the status reads timed-out, but nothing is stored.
	clock = clock.Add(2 * time.Minute)
	code, d, _ = check()
	if code != 0 || d["timedOut"] != 1.0 || d["pending"] != 0.0 || rowStatus(d, 0) != "timed-out" || d["changed"] != false {
		t.Fatalf("timed out: %d %v", code, d)
	}
	if st := registryDoc(t, dir)["escalations"].([]any)[0].(map[string]any)["status"]; st != "pending" {
		t.Errorf("timed-out must not be stored, status is %v", st)
	}

	// A late answer still lands.
	f.add("go with option two")
	code, d, _ = check()
	row := d["escalations"].([]any)[0].(map[string]any)
	ans, _ := row["answer"].(map[string]any)
	if code != 0 || d["answered"] != 1.0 || d["changed"] != true || row["status"] != "answered" ||
		ans["commentId"] != "4" || ans["author"] != "maint" || ans["body"] != "go with option two" || ans["seenAt"] != "2026-10-03T12:02:00Z" {
		t.Fatalf("answered: %d %v", code, d)
	}
	stored := registryDoc(t, dir)["escalations"].([]any)[0].(map[string]any)
	if stored["status"] != "answered" || stored["answer"] == nil {
		t.Errorf("stored: %v", stored)
	}

	// Answered entries need no forge call; the default view skips them.
	before := len(f.calls)
	code, d, _ = check()
	if code != 0 || len(d["escalations"].([]any)) != 0 || d["changed"] != false {
		t.Errorf("default view: %d %v", code, d)
	}
	code, d, _ = check("e1")
	if code != 0 || rowStatus(d, 0) != "answered" || d["changed"] != false || len(f.calls) != before {
		t.Errorf("named answered: %d %v (calls %v)", code, d, f.calls[before:])
	}
	if code, _, _ := check("e1", "e5"); code != 3 {
		t.Errorf("unknown id: %d, want 3", code)
	}
	if code, _, _ := rotaInWith(t, deps, t.TempDir(), "round", "escalate", "check"); code != 3 {
		t.Errorf("no project root: %d, want 3", code)
	}
}

func TestEscalateCheckForgeFailureAndDeletedComment(t *testing.T) {
	dir := escProject(t, ghCfg)
	f := &fakeThread{}
	deps := useThread(t, f)
	clock := time.Now().UTC()
	useEscalation(deps, &escHost{}, "", &clock)
	rotaInWith(t, deps, dir, "round", "escalate", "send", "7", "--title", "A", "--body-file", "body.md")
	rotaInWith(t, deps, dir, "round", "escalate", "send", "8", "--title", "B", "--body-file", "body.md")

	f.failRead = errors.New("HTTP 500: boom")
	code, out, errOut := rotaInWith(t, deps, dir, "round", "escalate", "check", "--json")
	d := data(t, out)
	if code != 0 || d["pending"] != 2.0 || !strings.Contains(errOut, "e1: cannot read issue #7") {
		t.Errorf("forge failure is a warning: %d %v %s", code, d, errOut)
	}
	f.failRead = nil

	// e1's comment is deleted, e2's thread still has it and an answer.
	f.remove("1")
	f.add("yes")
	code, out, errOut = rotaInWith(t, deps, dir, "round", "escalate", "check", "--json")
	d = data(t, out)
	rows := d["escalations"].([]any)
	if code != 0 || d["pending"] != 1.0 || d["answered"] != 1.0 || d["changed"] != true ||
		rows[0].(map[string]any)["status"] != "pending" || rows[1].(map[string]any)["status"] != "answered" ||
		!strings.Contains(errOut, "e1: escalation comment 1 is gone") {
		t.Errorf("deleted comment: %d %v %s", code, d, errOut)
	}
}
