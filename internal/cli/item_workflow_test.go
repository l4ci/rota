package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

func flowFixture() *trackertest.Fake {
	f := issueFixture()
	f.Milestones = []string{"M02 — Sharing"}
	return f
}

func list(o *jsonx.Object, k string) []any { v, _ := get(o, k).([]any); return v }

func runOK(t *testing.T, root string, want int, argv ...string) *jsonx.Object {
	t.Helper()
	return runOKWith(t, testDeps(), root, want, argv...)
}

// runOKWith is runOK with the given deps.
func runOKWith(t *testing.T, deps *Deps, root string, want int, argv ...string) *jsonx.Object {
	t.Helper()
	code, env, stderr := rotaRunWith(t, deps, append([]string{"--json", "-C", root}, argv...)...)
	if code != want {
		t.Fatalf("%v: exit %d, want %d\n%s\n%v", argv, code, want, stderr, env)
	}
	d, _ := env["data"].(*jsonx.Object)
	return d
}

func TestItemStateClaimReleaseShow(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	fake := flowFixture()
	deps := withTracker(t, fake)

	d := runOKWith(t, deps, root, 0, "item", "show", "F7")
	if get(d, "id") != "7" || get(d, "type") != "F" || get(d, "title") != "Add export" || get(d, "status") != "open" ||
		get(d, "state") != nil || get(d, "claimedBy") != nil || get(d, "milestone") != "M02" ||
		len(list(d, "notes")) != 0 || len(list(d, "comments")) != 0 || len(list(d, "assignees")) != 0 {
		t.Fatalf("show: %v", d)
	}
	// Text mode prints what the old helper printed.
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	var out, errb bytes.Buffer
	if code := mainWith(deps, []string{"-C", root, "item", "show", "7"}, strings.NewReader(""), &out, &errb); code != 0 ||
		out.String() != "[F7] Add export\ntype: feature\nstatus: open\nstate: none\nclaimed by: none\nassignee: none\nmilestone: M02\nnotes: none\ncomments: 0\n" {
		t.Fatalf("text show: exit %d\n%s%s", code, out.String(), errb.String())
	}

	d = runOKWith(t, deps, root, 0, "item", "state", "#7", "--to", "needs-review")
	if get(d, "id") != "7" || get(d, "type") != "F" || get(d, "state") != "needs-review" || get(d, "changed") != true {
		t.Fatalf("state: %v", d)
	}
	if d = runOKWith(t, deps, root, 0, "item", "state", "7", "--to", "needs-review"); get(d, "changed") != false {
		t.Fatalf("same state again: %v", d)
	}
	if d = runOKWith(t, deps, root, 0, "item", "state", "7", "--to", "none"); get(d, "state") != nil || get(d, "changed") != true {
		t.Fatalf("none: %v", d)
	}
	runOKWith(t, deps, root, ExitUsage, "item", "state", "7", "--to", "bogus")
	runOKWith(t, deps, root, ExitUsage, "item", "state", "7")

	d = runOKWith(t, deps, root, 0, "item", "claim", "7", "--as", "alice")
	if get(d, "id") != "7" || get(d, "type") != "F" || get(d, "claimId") != "alice" || get(d, "changed") != true {
		t.Fatalf("claim: %v", d)
	}
	d = runOKWith(t, deps, root, ExitRefused, "item", "claim", "7", "--as", "bob")
	if get(d, "blockedBy") != "claimed" || get(d, "changed") != true {
		t.Fatalf("lost claim: %v", d)
	}
	d = runOKWith(t, deps, root, 0, "item", "show", "7")
	if get(d, "claimedBy") != "alice" || get(d, "state") != "in-progress" || !reflect.DeepEqual(list(d, "assignees"), []any{"fake-user"}) {
		t.Fatalf("show after claim: %v", d)
	}
	runOKWith(t, deps, root, ExitResolution, "item", "claim", "9", "--as", "alice") // closed
	for _, as := range []string{"", "a b", "a-->b"} {
		runOKWith(t, deps, root, ExitUsage, "item", "claim", "7", "--as", as)
		runOKWith(t, deps, root, ExitUsage, "item", "release", "7", "--as", as)
	}
	if d = runOKWith(t, deps, root, 0, "item", "release", "7", "--as", "nobody"); get(d, "changed") != false {
		t.Fatalf("stranger release: %v", d)
	}
	if d = runOKWith(t, deps, root, 0, "item", "release", "F7", "--as", "alice"); get(d, "changed") != true || get(d, "id") != "7" {
		t.Fatalf("release: %v", d)
	}
}

func TestItemNotes(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	deps := withTracker(t, flowFixture())
	body := filepath.Join(t.TempDir(), "note.md")
	os.WriteFile(body, []byte("# Plan\n\nstep one\n"), 0o644)
	empty := filepath.Join(t.TempDir(), "empty.md")
	os.WriteFile(empty, []byte(" \n"), 0o644)

	d := runOKWith(t, deps, root, 0, "item", "note", "show", "F7", "--kind", "plan")
	if get(d, "exists") != false || get(d, "body") != "" || get(d, "id") != "7" || get(d, "kind") != "plan" {
		t.Fatalf("absent: %v", d)
	}
	d = runOKWith(t, deps, root, 0, "item", "note", "add", "7", "--kind", "plan", "--body-file", body)
	if get(d, "changed") != true || get(d, "type") != "F" {
		t.Fatalf("add: %v", d)
	}
	if d = runOKWith(t, deps, root, 0, "item", "note", "add", "7", "--kind", "plan", "--body-file", body); get(d, "changed") != false {
		t.Fatalf("add again: %v", d)
	}
	d = runOKWith(t, deps, root, 0, "item", "note", "show", "7", "--kind", "plan")
	if get(d, "exists") != true || get(d, "body") != "# Plan\n\nstep one" {
		t.Fatalf("show: %v", d)
	}
	if s := runOKWith(t, deps, root, 0, "item", "show", "7"); !reflect.DeepEqual(list(s, "notes"), []any{"plan"}) {
		t.Fatalf("show notes: %v", s)
	}
	if d = runOKWith(t, deps, root, 0, "item", "note", "rm", "7", "--kind", "plan"); get(d, "changed") != true {
		t.Fatalf("rm: %v", d)
	}
	if d = runOKWith(t, deps, root, 0, "item", "note", "rm", "7", "--kind", "plan"); get(d, "changed") != false {
		t.Fatalf("rm again: %v", d)
	}
	runOKWith(t, deps, root, ExitUsage, "item", "note", "add", "7", "--kind", "plan:S01", "--body-file", body)
	runOKWith(t, deps, root, ExitUsage, "item", "note", "show", "7", "--kind", "nope")
	runOKWith(t, deps, root, ExitUsage, "item", "note", "add", "7", "--kind", "plan", "--body-file", empty)
	runOKWith(t, deps, root, ExitUsage, "item", "note", "add", "7", "--kind", "plan")
	runOKWith(t, deps, root, ExitResolution, "item", "note", "add", "7", "--kind", "plan", "--body-file", filepath.Join(t.TempDir(), "missing"))
	runOKWith(t, deps, root, ExitResolution, "item", "note", "add", "99", "--kind", "plan", "--body-file", body)
}

// File mode: claim, release and state are no-ops (changed false) for an item
// that exists; show and the notes are refused as issue-only.
func TestWorkflowVerbsFileMode(t *testing.T) {
	root := trackerProject(t, "")
	body := filepath.Join(t.TempDir(), "n.md")
	os.WriteFile(body, []byte("x"), 0o644)
	for _, argv := range [][]string{
		{"item", "claim", "B01", "--as", "a"},
		{"item", "release", "B01", "--as", "a"},
		{"item", "state", "B01", "--to", "in-progress"},
	} {
		d := runOK(t, root, 0, argv...)
		if get(d, "id") != "B01" || get(d, "type") != "B" || get(d, "changed") != false {
			t.Errorf("%v: %v", argv, d)
		}
		unknown := append([]string{}, argv...)
		unknown[2] = "B99"
		runOK(t, root, ExitResolution, unknown...)
	}
	// Issue-only verbs under the file backend: read-only ones exit 1, mutating
	// ones 4; both carry {blockedBy: backend, changed: false}.
	for _, c := range []struct {
		exit int
		argv []string
	}{
		{ExitFailed, []string{"item", "show", "B01"}},
		{ExitFailed, []string{"item", "note", "show", "B01", "--kind", "plan"}},
		{ExitRefused, []string{"item", "note", "add", "B01", "--kind", "plan", "--body-file", body}},
		{ExitRefused, []string{"item", "note", "rm", "B01", "--kind", "plan"}},
	} {
		d := runOK(t, root, c.exit, c.argv...)
		if get(d, "blockedBy") != "backend" || get(d, "changed") != false {
			t.Errorf("%v: %v", c.argv, d)
		}
	}
}

// Tracker failures exit through their kind: rate limit 6, forge unavailable 5,
// a missing object 3, anything the CLI cannot start 70; the message is kept.
func TestTrackerErrorsMapToExits(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	for _, c := range []struct {
		kind tracker.Kind
		exit int
	}{
		{tracker.KindRateLimited, ExitRetry}, {tracker.KindUnavailable, ExitUnavailable},
		{tracker.KindNotFound, ExitResolution}, {tracker.KindInternal, ExitInternal}, {tracker.KindFailed, ExitUnavailable},
	} {
		fake := flowFixture()
		fake.Fail = map[string]error{"comments": &tracker.Error{Kind: c.kind, Code: 1, Message: "forge said no"}}
		deps := withTracker(t, fake)
		code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "item", "show", "7")
		if code != c.exit || !strings.Contains(stderr, "forge said no") || env["ok"] != false {
			t.Errorf("kind %v: exit %d, want %d (%s)", c.kind, code, c.exit, stderr)
		}
	}
}
