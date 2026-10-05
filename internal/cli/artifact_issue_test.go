package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bodyFile(t *testing.T, text string) string {
	p := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func issueRun(t *testing.T, root string, args ...string) (int, map[string]any, string) {
	t.Helper()
	return issueRunWith(t, testDeps(), root, args...)
}

// issueRunWith is issueRun with the given deps.
func issueRunWith(t *testing.T, deps *Deps, root string, args ...string) (int, map[string]any, string) {
	t.Helper()
	return rotaRunWith(t, deps, append([]string{"--json", "-C", root}, args...)...)
}

func ddata(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	o := issueData(t, env)
	m := map[string]any{}
	for _, k := range o.Keys() {
		m[k] = issueGet(o, k)
	}
	return m
}

func TestIssueModeDesign(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	deps := withTracker(t, issueFixture())
	code, env, stderr := issueRunWith(t, deps, root, "design", "add", "F7", "--title", "Export design")
	if d := ddata(t, env); code != 0 || d["id"] != "7" || d["type"] != "F" || d["changed"] != true {
		t.Fatalf("add: %d %v %s", code, env, stderr)
	}
	for _, ref := range []string{"7", "#7"} {
		if code, _, _ := issueRunWith(t, deps, root, "design", "add", ref, "--title", "x"); code != 4 {
			t.Errorf("add %s resolves the item whose design exists: %d, want 4", ref, code)
		}
		_, env, _ := issueRunWith(t, deps, root, "design", "show", ref)
		if body, _ := ddata(t, env)["body"].(string); !strings.HasPrefix(body, "---\nid: F7\n") {
			t.Errorf("show %s: %q", ref, body)
		}
	}
	code, env, stderr = issueRunWith(t, deps, root, "design", "add", "x7", "--title", "x")
	if code != 2 || !strings.Contains(stderr+strings.Join(keysOf(env), ","), "N, #N") {
		t.Errorf("bad shape: %d %s, want exit 2 naming N/#N", code, stderr)
	}
	code, env, _ = issueRunWith(t, deps, root, "design", "add", "F7", "--title", "again")
	if code != 4 || !strings.Contains(strings.Join(keysOf(env), ","), "data") {
		t.Errorf("duplicate: %d %v", code, env)
	}
	if code, _, _ := issueRunWith(t, deps, root, "design", "add", "F99", "--title", "x"); code != 3 {
		t.Errorf("unknown item: %d, want 3", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "design", "add", "S01", "--title", "x"); code != 2 {
		t.Errorf("S01: %d, want 2", code)
	}
	_, env, _ = issueRunWith(t, deps, root, "design", "show", "F7")
	body, _ := ddata(t, env)["body"].(string)
	if !strings.HasPrefix(body, "---\nid: F7\ntitle: Export design\nstatus: draft\n") || !strings.HasSuffix(body, "\n") {
		t.Errorf("show body: %q", body)
	}
	_, env, _ = issueRunWith(t, deps, root, "design", "put", "F7", "--body-file", bodyFile(t, "# new\n"))
	if ddata(t, env)["changed"] != true {
		t.Errorf("put: %v", env)
	}
	_, env, _ = issueRunWith(t, deps, root, "design", "put", "F7", "--body-file", bodyFile(t, "# new\n"))
	if ddata(t, env)["changed"] != false {
		t.Errorf("identical put: %v", env)
	}
	if code, _, _ := issueRunWith(t, deps, root, "design", "rm", "F7"); code != 0 {
		t.Errorf("rm: %d", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "design", "rm", "F7"); code != 3 {
		t.Errorf("second rm: %d", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "design", "show", "F7"); code != 3 {
		t.Errorf("show after rm: %d", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "design", "put", "F7", "--body-file", bodyFile(t, "x")); code != 3 {
		t.Errorf("put without a note: %d", code)
	}
	// File-only verbs under issue mode: a mutating one is refused (4), a
	// read-only one fails (1); both carry {blockedBy: backend, changed: false}.
	for _, c := range []struct {
		want int
		args []string
	}{
		{1, []string{"design", "list"}},
		{4, []string{"design", "amend", "F7", "--section", "Goal", "--mode", "append", "--body-file", bodyFile(t, "x")}},
	} {
		code, env, _ := issueRunWith(t, deps, root, c.args...)
		d := ddata(t, env)
		if code != c.want || d["blockedBy"] != "backend" || d["changed"] != false {
			t.Errorf("%v: exit %d data %v, want %d with blockedBy backend", c.args, code, d, c.want)
		}
	}
}

func keysOf(m map[string]any) []string {
	var k []string
	for key := range m {
		k = append(k, key)
	}
	return k
}

func TestIssueModePlan(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	deps := withTracker(t, issueFixture())
	code, env, stderr := issueRunWith(t, deps, root, "plan", "add", "M02-F7", "--title", "Export plan", "--design", "F07")
	if code != 0 {
		t.Fatalf("add: exit %d: %s", code, stderr)
	}
	if d := ddata(t, env); code != 0 || d["key"] != "M02-F7" || d["unitKind"] != "item" || d["changed"] != true {
		t.Fatalf("add: %d %v %s", code, env, stderr)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "add", "M02-F7", "--title", "again"); code != 4 {
		t.Errorf("duplicate: %d", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "add", "M02-F99", "--title", "x"); code != 3 {
		t.Errorf("unknown item: %d", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "add", "M02-F7"); code != 2 {
		t.Errorf("missing title: %d", code)
	}
	_, env, _ = issueRunWith(t, deps, root, "plan", "show", "M02-F7")
	body, _ := ddata(t, env)["body"].(string)
	if !strings.Contains(body, "unit: F7\nunitKind: item\ndesign: note:design\ntitle: Export plan\n") {
		t.Errorf("show body: %q", body)
	}
	// The milestone part of an item key is ignored.
	if _, env, _ = issueRunWith(t, deps, root, "plan", "show", "M09-F7"); ddata(t, env)["body"] != body {
		t.Error("item plan lookup depends on the milestone part")
	}
	_, env, _ = issueRunWith(t, deps, root, "plan", "put", "M02-F7", "--body-file", bodyFile(t, "# replaced\n"))
	if ddata(t, env)["changed"] != true {
		t.Errorf("put: %v", env)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "rm", "M02-F7"); code != 0 {
		t.Errorf("rm: %d", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "rm", "M02-F7"); code != 3 {
		t.Errorf("second rm: %d", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "add", "--slice", "--title", "t"); code != 2 {
		t.Errorf("argument errors stay exit 2: %d", code)
	}
}

func TestIssueModePlanWithoutMilestone(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	deps := withTracker(t, issueFixture())
	code, env, stderr := issueRunWith(t, deps, root, "plan", "add", "#7", "--title", "Export plan")
	if d := ddata(t, env); code != 0 || d["key"] != "#7" || d["unitKind"] != "item" {
		t.Fatalf("add: %d %v %s", code, env, stderr)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "add", "F7", "--title", "again"); code != 4 {
		t.Errorf("duplicate under another spelling: %d", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "add", "#99", "--title", "x"); code != 3 {
		t.Errorf("unknown item: %d", code)
	}
	_, env, _ = issueRunWith(t, deps, root, "plan", "show", "#7")
	if body, _ := ddata(t, env)["body"].(string); !strings.Contains(body, "unitKind: item") {
		t.Errorf("show body: %q", body)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "put", "#7", "--body-file", bodyFile(t, "# replaced\n")); code != 0 {
		t.Errorf("put: %d", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "rm", "#7"); code != 0 {
		t.Errorf("rm: %d", code)
	}
}

func TestIssueModeProofAndUncertain(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	deps := withTracker(t, issueFixture())
	code, env, stderr := issueRunWith(t, deps, root, "proof", "add", "7", "--check", "unit tests", "--result", "PASS", "--evidence", "ok", "--sha", "abc")
	if d := ddata(t, env); code != 0 || d["id"] != "7" || d["type"] != "F" || d["changed"] != true || d["sha"] != "abc" {
		t.Fatalf("add: %d %v %s", code, env, stderr)
	}
	_, env, _ = issueRunWith(t, deps, root, "proof", "add", "#7", "--check", "unit tests", "--result", "PASS", "--evidence", "ok", "--sha", "abc")
	if ddata(t, env)["changed"] != false {
		t.Errorf("repeat: %v", env)
	}
	issueRunWith(t, deps, root, "proof", "add", "F7", "--check", "lint", "--result", "FAIL", "--evidence", "a · b", "--sha", "-")
	_, env, _ = issueRunWith(t, deps, root, "proof", "show", "7")
	d := ddata(t, env)
	if fmt.Sprint(d["count"]) != "2" {
		t.Errorf("count: %v", d["count"])
	}
	if code, _, _ := issueRunWith(t, deps, root, "proof", "add", "99", "--check", "c", "--result", "PASS", "--evidence", "e"); code != 3 {
		t.Errorf("unknown item: %d", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "proof", "show", "99"); code != 3 {
		t.Errorf("show unknown: %d", code)
	}
	_, env, _ = issueRunWith(t, deps, root, "proof", "show", "3")
	_ = env
	code, env, _ = issueRunWith(t, deps, root, "plan", "uncertain", "7")
	d = ddata(t, env)
	if code != 0 || d["uncertain"] != true || d["id"] != "7" || d["type"] != "F" {
		t.Errorf("uncertain: %d %v", code, env)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "uncertain", "9"); code != 3 {
		t.Errorf("closed item: %d, want 3", code)
	}
	if code, _, _ := issueRunWith(t, deps, root, "plan", "uncertain", "99"); code != 3 {
		t.Errorf("unknown: %d", code)
	}
}

// An invalid backlog.backend is a corrupt config for every verb: design and
// milestone verbs exit 70 rather than quietly running in file mode.
func TestInvalidBackendExits70(t *testing.T) {
	root := trackerProject(t, `{"backlog": {"backend": "isssues"}}`)
	for _, argv := range [][]string{
		{"design", "add", "B01", "--title", "T"},
		{"milestone", "list"},
		{"item", "show", "B01"},
	} {
		code, env, stderr := rotaRun(t, append([]string{"--json", "-C", root}, argv...)...)
		if code != ExitInternal || env["ok"] != false || !strings.Contains(stderr, "invalid backlog.backend") {
			t.Errorf("%v: code=%d env=%v stderr=%s", argv, code, env, stderr)
		}
	}
}
