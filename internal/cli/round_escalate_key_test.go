package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/escalation"
	"github.com/l4ci/rota/internal/gate"
	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/worker"
)

// keyForge is a Jira-shaped tracker: issues are PROJ-123, and it refuses a
// number it does not know. It keeps each issue's comments.
type keyForge struct {
	threads map[tracker.IssueKey][]tracker.Comment
	next    int
	posted  []tracker.IssueKey
}

func newKeyForge(keys ...tracker.IssueKey) *keyForge {
	f := &keyForge{threads: map[tracker.IssueKey][]tracker.Comment{}}
	for _, k := range keys {
		f.threads[k] = nil
	}
	return f
}

func (f *keyForge) known(k tracker.IssueKey) error {
	if _, ok := f.threads[k]; !ok {
		return &tracker.Error{Kind: tracker.KindNotFound, Code: 1, Message: "issue " + k.Ref() + " not found"}
	}
	return nil
}

func (f *keyForge) Comments(_ context.Context, k tracker.IssueKey) ([]tracker.Comment, error) {
	return f.threads[k], f.known(k)
}

func (f *keyForge) AddComment(_ context.Context, k tracker.IssueKey, body string) (string, error) {
	if err := f.known(k); err != nil {
		return "", err
	}
	f.next++
	id := strconv.Itoa(f.next)
	f.threads[k] = append(f.threads[k], tracker.Comment{ID: id, Body: body, Author: "bot"})
	f.posted = append(f.posted, k)
	return id, nil
}

func (f *keyForge) CommentURL(_ context.Context, k tracker.IssueKey, id string) (string, error) {
	return "https://jira.example/browse/" + k.String() + "#comment-" + id, nil
}

func (f *keyForge) MRNotes(context.Context, int) ([]tracker.Comment, error) { return nil, nil }
func (f *keyForge) AddMRNote(context.Context, int, string) (string, error)  { return "", nil }
func (f *keyForge) PRCommentURL(context.Context, int, string) (string, error) {
	return "", nil
}

func useKeyForge(deps *Deps, f escalation.Forge, h *escHost, clock *time.Time) {
	useEscalation(deps, h, "1", clock)
	base := deps.EscalationEnv
	deps.EscalationEnv = func() escalation.Env {
		e := base()
		e.Forge = func(context.Context, string) (escalation.Forge, error) { return f, nil }
		return e
	}
}

// AC-1/AC-5: a key issue escalates by key, the record stores the key, and no
// output spells it `#PROJ-123` or `number: 0`.
func TestEscalateSendKeyIssue(t *testing.T) {
	dir := escProject(t, ghCfg)
	f := newKeyForge("PROJ-123")
	deps := testDeps()
	h := &escHost{cliHost: cliHost{herdr: true}}
	clock := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	useKeyForge(deps, f, h, &clock)

	code, out, errOut := rotaInWith(t, deps, dir, "round", "escalate", "send", "proj-123", "--title", "Pick one", "--body-file", "body.md", "--json")
	if code != 0 {
		t.Fatalf("send: %d %s %s", code, out, errOut)
	}
	d := data(t, out)
	e := d["escalation"].(map[string]any)
	if e["kind"] != "issue" || e["key"] != "PROJ-123" {
		t.Errorf("entry: %v", e)
	}
	if _, has := e["number"]; has {
		t.Errorf("a key issue has no number: %v", e)
	}
	if len(f.posted) != 1 || f.posted[0] != "PROJ-123" {
		t.Errorf("posted on %v", f.posted)
	}
	if d["url"] != "https://jira.example/browse/PROJ-123#comment-1" {
		t.Errorf("url %v", d["url"])
	}
	if len(h.notes) != 1 || h.notes[0] != "rota escalation e1|Pick one (PROJ-123)" {
		t.Errorf("notification: %v", h.notes)
	}
	for _, s := range []string{out, errOut, strings.Join(h.notes, "\n")} {
		if strings.Contains(s, "#PROJ-123") || strings.Contains(s, `"number": 0`) || strings.Contains(s, `"number":0`) || strings.Contains(s, "#0") {
			t.Errorf("a key issue prints as a number: %s", s)
		}
	}

	// one pending escalation per thread, spelled as the key
	code, out, errOut = rotaInWith(t, deps, dir, "round", "escalate", "send", "PROJ-123", "--title", "Again", "--body-file", "body.md")
	if code != 4 || !strings.Contains(errOut, "still pending on issue PROJ-123") {
		t.Errorf("second send: %d %s %s", code, out, errOut)
	}

	// check finds the answer on the key thread and reports the key
	f.threads["PROJ-123"] = append(f.threads["PROJ-123"], tracker.Comment{ID: "9", Author: "maint", Body: "approve"})
	code, out, errOut = rotaInWith(t, deps, dir, "round", "escalate", "check", "--json")
	if code != 0 {
		t.Fatalf("check: %d %s %s", code, out, errOut)
	}
	rows := data(t, out)["escalations"].([]any)
	row := rows[0].(map[string]any)
	if row["status"] != "answered" || row["key"] != "PROJ-123" {
		t.Errorf("row: %v", row)
	}
	if _, has := row["number"]; has {
		t.Errorf("row has a number: %v", row)
	}
	code, out, _ = rotaInWith(t, deps, dir, "round", "escalate", "check", "e1")
	if !strings.Contains(out, "issue PROJ-123") || strings.Contains(out, "#PROJ-123") {
		t.Errorf("text: %q", out)
	}
}

func TestEscalateSendKeyUsage(t *testing.T) {
	dir := escProject(t, ghCfg)
	deps := testDeps()
	clock := time.Now().UTC()
	useKeyForge(deps, newKeyForge("PROJ-123"), &escHost{cliHost: cliHost{herdr: true}}, &clock)
	for _, args := range [][]string{
		{"send", "PROJ-123", "--pr", "--title", "t", "--body-file", "body.md"}, // a PR is a number
		{"send", "proj_123", "--title", "t", "--body-file", "body.md"},
		{"send", "+7", "--title", "t", "--body-file", "body.md"},
	} {
		if code, out, errOut := rotaInWith(t, deps, dir, append([]string{"round", "escalate"}, args...)...); code != 2 {
			t.Errorf("%v: %d %s %s", args, code, out, errOut)
		}
	}
	code, _, errOut := rotaInWith(t, deps, dir, "round", "escalate", "send", "PROJ-999", "--title", "t", "--body-file", "body.md")
	if code != 3 || !strings.Contains(errOut, "no such issue PROJ-999") {
		t.Errorf("unknown key: %d %s", code, errOut)
	}
}

// AC-2: --approval and --escalate on a slot that holds PROJ-123 and has no PR
// post to the issue's thread, and --approval reads the answer back from it.
func TestApprovalThreadForKeyIssue(t *testing.T) {
	dir := escProject(t, ghCfg)
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	target := worker.GateTarget{Name: "ben", Branch: "ben/PROJ-123-widen", Issue: "PROJ-123"}
	th, err := targetApprovalThread(target)
	if err != nil {
		t.Fatal(err)
	}
	if th.Kind != "issue" || th.Key != "PROJ-123" || th.Number != 0 || th.Ref() != "PROJ-123" || th.Slot != "ben" {
		t.Fatalf("thread: %+v", th)
	}
	if num, err := targetApprovalThread(worker.GateTarget{Name: "ben", Issue: "804"}); err != nil || num.Key != "" || num.Number != 804 || num.Ref() != "#804" {
		t.Errorf("numbered issue: %+v %v", num, err)
	}
	if _, err := targetApprovalThread(worker.GateTarget{Name: "ben", Issue: "B07"}); err == nil {
		t.Error("an item id is not a thread")
	}

	write(t, filepath.Join(dir, ".rota", "workers.json"), `{"slots":[{"name":"ben","branch":"ben/PROJ-123-widen","task":"PROJ-123"}]}`)
	f := newKeyForge("PROJ-123")
	deps := testDeps()
	clock := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	useKeyForge(deps, f, &escHost{cliHost: cliHost{herdr: true}}, &clock)
	var warn strings.Builder
	c := &Ctx{Path: "rota worker gate", Stderr: &warn, Deps: deps}

	got, ok := approvalEscalate(c, gate.MergePolicy{Mode: gate.MergeAll}, nil, th).(interface{ Get(string) (any, bool) })
	if !ok {
		t.Fatalf("no escalation sent: %s", warn.String())
	}
	if k, _ := got.Get("key"); k != "PROJ-123" {
		t.Errorf("entry key %v", k)
	}
	if len(f.posted) != 1 || f.posted[0] != "PROJ-123" {
		t.Fatalf("posted on %v", f.posted)
	}
	// a second --escalate reuses the pending request instead of posting again
	approvalEscalate(c, gate.MergePolicy{Mode: gate.MergeAll}, nil, th)
	if len(f.posted) != 1 {
		t.Errorf("posted again: %v", f.posted)
	}

	// --approval: pending, then answered
	if _, res, err := approvalConfirm(c, "e1", th, nil); err == nil || res.Data == nil {
		t.Errorf("pending escalation approved a merge: %v", err)
	}
	f.threads["PROJ-123"] = append(f.threads["PROJ-123"], tracker.Comment{ID: "9", Author: "maint", Body: "approve"})
	if _, err := escalation.Check(context.Background(), deps.escalationEnv(), dir, nil); err != nil {
		t.Fatal(err)
	}
	conf, _, err := approvalConfirm(c, "e1", th, nil)
	if err != nil || !conf.Given || conf.Escalation != "e1" {
		t.Errorf("answered: %+v %v", conf, err)
	}
	// the same escalation is not an approval for another issue's thread
	other := th
	other.Key = "PROJ-124"
	if _, _, err := approvalConfirm(c, "e1", other, nil); err == nil || !strings.Contains(err.Error(), "is on issue PROJ-123, not on this merge's approval thread (issue PROJ-124)") {
		t.Errorf("wrong thread: %v", err)
	}
}

// AC-5: the release gate names a key issue by its key, a numbered one as today.
func TestIssueRowKeyAndNumber(t *testing.T) {
	num := issueRow(tracker.Issue{Key: tracker.NumKey(7), Title: "t"}, "label", "x")
	if k := strings.Join(num.Keys(), ","); k != "number,title,label" {
		t.Errorf("numbered keys: %s", k)
	}
	if n, _ := num.Get("number"); n != 7 {
		t.Errorf("number: %v", n)
	}
	key := issueRow(tracker.Issue{Key: "PROJ-123", Title: "t"})
	if k := strings.Join(key.Keys(), ","); k != "key,title" {
		t.Errorf("key keys: %s", k)
	}
}

// AC-3: `rota issues label|close` read a key, and a tracker that has no such
// issue answers exit 3, not a usage error.
func TestIssuesVerbsTakeAKey(t *testing.T) {
	dir := escProject(t, ghCfg)
	deps := useThread(t, &fakeThread{})
	for _, args := range [][]string{
		{"issues", "label", "PROJ-123", "--add", "x"},
		{"issues", "close", "PROJ-123", "--commit", "HEAD"},
	} {
		if code, out, errOut := rotaInWith(t, deps, dir, args...); code == 2 {
			t.Errorf("%v refused the key: %d %s %s", args, code, out, errOut)
		}
	}
	if code, _, _ := rotaInWith(t, deps, dir, "issues", "label", "not a key", "--add", "x"); code != 2 {
		t.Errorf("garbage: exit %d", code)
	}
}
