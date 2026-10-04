package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// seedEscalations writes the escalations list into .rota/workers.json, keeping
// whatever else the registry holds.
func seedEscalations(t *testing.T, root string, list ...map[string]any) {
	t.Helper()
	doc := registryDoc(t, root)
	doc["escalations"] = list
	b, _ := json.Marshal(doc)
	write(t, filepath.Join(root, ".rota", "workers.json"), string(b))
}

func escEntry(id, kind string, n int, status, answer string) map[string]any {
	e := map[string]any{"id": id, "kind": kind, "number": n, "title": "Merge approval", "commentId": "1",
		"sentAt": "2026-10-03T09:00:00Z", "notified": false, "status": status}
	if status == "answered" {
		e["answer"] = map[string]any{"commentId": "2", "author": "maint", "body": answer, "seenAt": "2026-10-03T09:30:00Z"}
	}
	return e
}

func posts(f *fakeThread) int {
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, "-X POST") {
			n++
		}
	}
	return n
}

const prAllCfg = `{"backlog":{"backend":"issues"},"issues":{"provider":"github"}}`

// prMergeProject is an issue-mode project with ship.mergeApproval set, a fake
// forge for the PRs and a fake comment thread for the escalations.
func prMergeProject(t *testing.T, ship map[string]any) (string, *a8Forge, *fakeThread) {
	t.Helper()
	f := a8Fixture()
	root := a8Project(t, f)
	write(t, filepath.Join(root, ".rota", "config.json"), gateConfig(t, prAllCfg, "auto", ship))
	th := &fakeThread{}
	useThread(t, th)
	clock := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	useEscalation(t, &escHost{}, "", &clock)
	return root, f, th
}

func TestApprovalFlagsMutuallyExclusive(t *testing.T) {
	root, f, th := prMergeProject(t, map[string]any{"mergeApproval": "all"})
	for _, args := range [][]string{
		{"--approval", "e1", "--escalate"},
		{"--approval", "e1", "--confirm", "--confirm-note", "yes"},
		{"--escalate", "--confirm", "--confirm-note", "yes"},
	} {
		code, _, msg := a8Run(t, root, append([]string{"ship", "pr-merge", "10"}, args...)...)
		if code != 2 || !strings.Contains(msg, "mutually exclusive") || len(f.merged) != 0 || posts(th) != 0 || gateAudit(t, root) != nil {
			t.Errorf("%v: %d %s", args, code, msg)
		}
	}
	// the same on worker gate, before any other work
	if code, _, _ := rotaIn(t, t.TempDir(), "worker", "gate", "w1", "--base", "main", "--approval", "e1", "--escalate"); code != 2 {
		t.Errorf("worker gate: exit %d", code)
	}
}

func TestPRMergeApproval(t *testing.T) {
	root, f, _ := prMergeProject(t, map[string]any{"mergeApproval": "all"})
	late := escEntry("e3", "pr", 10, "pending", "")
	late["deadline"] = "2026-10-03T09:59:00Z"
	seedEscalations(t, root,
		escEntry("e1", "pr", 10, "answered", "Approve.\nthanks"),
		escEntry("e2", "pr", 10, "pending", ""),
		late,
		escEntry("e4", "pr", 10, "answered", "no, hold it"),
		escEntry("e5", "pr", 11, "answered", "yes"),
		escEntry("e6", "issue", 10, "answered", "yes"),
	)
	refuse := func(id string, wantCode int) map[string]any {
		t.Helper()
		code, d, msg := a8Run(t, root, "ship", "pr-merge", "10", "--approval", id)
		if code != wantCode || len(f.merged) != 0 || gateAudit(t, root) != nil {
			t.Fatalf("%s: %d %s merged %v audit %v", id, code, msg, f.merged, gateAudit(t, root))
		}
		return d
	}
	refuse("e9", 3)
	refuse("e5", 2)
	refuse("e6", 2)
	if d := refuse("e2", 4); d["blockedBy"] != "approval pending" || d["escalation"] != "e2" || d["status"] != "pending" || d["changed"] != false || d["pr"] != float64(10) {
		t.Errorf("pending: %v", d)
	}
	if d := refuse("e3", 4); d["blockedBy"] != "approval pending" || d["status"] != "timed-out" {
		t.Errorf("timed out: %v", d)
	}
	if d := refuse("e4", 4); d["blockedBy"] != "approval declined" || d["escalation"] != "e4" || d["answer"] != "no, hold it" || d["changed"] != false {
		t.Errorf("declined: %v", d)
	}
	code, _, msg := a8Run(t, root, "ship", "pr-merge", "10", "--approval", "e1")
	if code != 0 || !reflect.DeepEqual(f.merged, []int{10}) {
		t.Fatalf("approved: %d %s merged %v", code, msg, f.merged)
	}
	a := gateAudit(t, root)
	if len(a) != 1 || a[0]["note"] != "Approve.\nthanks" || a[0]["escalation"] != "e1" || a[0]["gate"] != "merge-approval" || a[0]["verb"] != "ship pr-merge" || a[0]["target"] != "PR 10" {
		t.Fatalf("audit %v", a)
	}
}

func TestPRMergeEscalate(t *testing.T) {
	root, f, th := prMergeProject(t, map[string]any{"mergeApproval": "all"})
	code, d, _ := a8Run(t, root, "ship", "pr-merge", "10", "--escalate")
	e, _ := d["escalation"].(map[string]any)
	if code != 4 || d["blockedBy"] != "manual gate" || d["pr"] != float64(10) || e["id"] != "e1" || e["kind"] != "pr" || e["number"] != float64(10) || e["status"] != "pending" {
		t.Fatalf("first: %d %v", code, d)
	}
	if posts(th) != 1 || len(f.merged) != 0 {
		t.Fatalf("posts %d merged %v", posts(th), f.merged)
	}
	body := th.comments[0]["body"].(string)
	if !strings.Contains(body, "Merge approval: PR #10") || !strings.Contains(body, "`all`") ||
		!strings.Contains(body, "Reply `approve`, `approved`, `yes`, `lgtm` or `ship it` to merge. Any other reply holds the merge.") {
		t.Errorf("body: %s", body)
	}
	// a re-gate reuses the pending escalation and posts nothing
	code, d, _ = a8Run(t, root, "ship", "pr-merge", "10", "--escalate")
	if e, _ := d["escalation"].(map[string]any); code != 4 || e["id"] != "e1" || posts(th) != 1 {
		t.Fatalf("second: %d %v posts %d", code, d, posts(th))
	}
	if gateAudit(t, root) != nil {
		t.Error("a refusal audited")
	}
}

func TestPRMergeEscalatePathsAndSendFailure(t *testing.T) {
	root, f, th := prMergeProject(t, map[string]any{"mergeApproval": "paths", "mergeApprovalPaths": []any{"*.md"}})
	f.files = map[int][]string{10: {"src/a.go"}, 11: {"README.md"}}
	// not covered: both flags are accepted and do nothing
	if code, _, msg := a8Run(t, root, "ship", "pr-merge", "10", "--approval", "e99"); code != 0 {
		t.Fatalf("approval, not covered: %d %s", code, msg)
	}
	if code, _, _ := a8Run(t, root, "ship", "pr-merge", "12", "--escalate"); code != 0 || posts(th) != 0 || gateAudit(t, root) != nil {
		t.Fatalf("escalate, not covered: posts %d", posts(th))
	}
	code, d, _ := a8Run(t, root, "ship", "pr-merge", "11", "--escalate")
	if code != 4 || posts(th) != 1 || d["escalation"] == nil {
		t.Fatalf("covered: %d %v", code, d)
	}
	if body := th.comments[0]["body"].(string); !strings.Contains(body, "`paths`") || !strings.Contains(body, "- README.md") {
		t.Errorf("body: %s", body)
	}
	// a send failure is a warning; the refusal stands without an escalation
	root2, _, th2 := prMergeProject(t, map[string]any{"mergeApproval": "all"})
	th2.notFound = true
	o := trRun(t, root2, "", "--json", "ship", "pr-merge", "10", "--escalate")
	env := envelope(t, o.stdout)
	d2, _ := env["data"].(map[string]any)
	if o.code != 4 || d2["blockedBy"] != "manual gate" || d2["escalation"] != nil || !strings.Contains(o.stderr, "approval request not sent") {
		t.Fatalf("send failure: %+v", o)
	}
}

func TestSlotApprovalThread(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".rota", "workers.json"), `{"slots":[
{"name":"a","branch":"a/5-x","pr":"https://github.com/o/r/pull/42"},
{"name":"b","task":"9","branch":"b/3-y"},
{"name":"c","branch":"c/3-y"},
{"name":"d","branch":"park/d"},
{"name":"e","task":"do things","branch":"e/x"}]}`)
	for _, c := range []struct {
		slot, kind string
		n          int
		title      string
	}{
		{"a", "pr", 42, "Merge approval: PR #42"},
		{"b", "issue", 9, "Merge approval: b (b/3-y)"},
		{"c", "issue", 3, "Merge approval: c (c/3-y)"},
	} {
		th, err := slotApprovalThread(root, c.slot)
		if err != nil || th.Kind != c.kind || th.Number != c.n || th.Title != c.title || th.Slot != c.slot {
			t.Errorf("%s: %+v %v", c.slot, th, err)
		}
	}
	for _, s := range []string{"d", "e"} {
		if _, err := slotApprovalThread(root, s); err == nil || !strings.Contains(err.Error(), "approval thread") {
			t.Errorf("%s: %v", s, err)
		}
	}
	if _, err := slotApprovalThread(root, "zz"); err == nil {
		t.Error("unknown slot")
	}
}

func TestWorkerGateApprovalOnIssueThread(t *testing.T) {
	dir := workerProject(t, gateConfig(t, `{"refactor":{"verifyCommands":["test -f feature.txt"]},"issues":{"provider":"github"}}`, "auto", map[string]any{"mergeApproval": "all"}))
	rotaIn(t, dir, "worker", "pool", "init", "--slots", "1", "--base", "main")
	wt := filepath.Join(dir, ".worktrees", "w1")
	write(t, filepath.Join(wt, "feature.txt"), "f")
	gitT(t, wt, "add", "feature.txt")
	gitT(t, wt, "commit", "-q", "-m", "feature")
	th := &fakeThread{}
	useThread(t, th)
	clock := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	useEscalation(t, &escHost{}, "", &clock)
	merged := func() bool { _, err := os.Stat(filepath.Join(dir, "feature.txt")); return err == nil }
	run := func(args ...string) (int, map[string]any, string) {
		code, out, errOut := rotaIn(t, dir, append([]string{"--json", "worker", "gate", "w1", "--base", "main"}, args...)...)
		return code, data(t, out), errOut
	}

	// no PR and no issue number: no thread, exit 2
	if code, _, msg := run("--escalate"); code != 2 || !strings.Contains(msg, "approval thread") || posts(th) != 0 {
		t.Fatalf("no thread: %d %s", code, msg)
	}
	if code, _, _ := run("--approval", "e1"); code != 2 {
		t.Fatalf("no thread, approval: %d", code)
	}

	// give the slot an issue
	doc := registryDoc(t, dir)
	doc["slots"].([]any)[0].(map[string]any)["task"] = "7"
	b, _ := json.Marshal(doc)
	write(t, filepath.Join(dir, ".rota", "workers.json"), string(b))

	code, d, _ := run("--escalate")
	e, _ := d["escalation"].(map[string]any)
	if code != 4 || d["verdict"] != "approval-required" || d["blockedBy"] != "manual gate" || e["id"] != "e1" || e["kind"] != "issue" || e["number"] != float64(7) || e["slot"] != "w1" {
		t.Fatalf("escalate: %d %v", code, d)
	}
	if posts(th) != 1 || !strings.Contains(th.calls[0], "issues/7/comments") || !strings.Contains(th.comments[0]["body"].(string), "Merge approval: w1 (") {
		t.Fatalf("post: %v", th.calls)
	}
	if code, d, _ := run("--escalate"); code != 4 || posts(th) != 1 || d["escalation"].(map[string]any)["id"] != "e1" {
		t.Fatalf("re-gate: %d %v posts %d", code, d, posts(th))
	}
	// pending: exit 4 in the worker gate shape
	code, d, _ = run("--approval", "e1")
	if code != 4 || d["verdict"] != "approval-required" || d["blockedBy"] != "approval pending" || d["escalation"] != "e1" || d["status"] != "pending" || d["slot"] != "w1" || merged() {
		t.Fatalf("pending: %d %v", code, d)
	}
	// declined
	doc = registryDoc(t, dir)
	esc := doc["escalations"].([]any)[0].(map[string]any)
	esc["status"], esc["answer"] = "answered", map[string]any{"commentId": "2", "author": "maint", "body": "not yet", "seenAt": "2026-10-03T10:01:00Z"}
	b, _ = json.Marshal(doc)
	write(t, filepath.Join(dir, ".rota", "workers.json"), string(b))
	code, d, _ = run("--approval", "e1")
	if code != 4 || d["blockedBy"] != "approval declined" || d["answer"] != "not yet" || merged() {
		t.Fatalf("declined: %d %v", code, d)
	}
	// approved
	esc["answer"].(map[string]any)["body"] = "LGTM, ship"
	b, _ = json.Marshal(doc)
	write(t, filepath.Join(dir, ".rota", "workers.json"), string(b))
	if code, d, msg := run("--approval", "e1"); code != 0 || !merged() {
		t.Fatalf("approved: %d %v %s", code, d, msg)
	}
	a := gateAudit(t, dir)
	if len(a) != 1 || a[0]["note"] != "LGTM, ship" || a[0]["escalation"] != "e1" || a[0]["verb"] != "worker gate" {
		t.Fatalf("audit %v", a)
	}
}
