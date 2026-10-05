package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
)

// withTracker returns deps whose NewTracker serves tr.
func withTracker(t *testing.T, tr backlog.Tracker) *Deps {
	t.Helper()
	deps := testDeps()
	deps.NewTracker = func(context.Context, string, any) (backlog.Tracker, error) { return tr, nil }
	return deps
}

// rotaRunWith is rotaRun with the given deps.
func rotaRunWith(t *testing.T, deps *Deps, args ...string) (int, map[string]any, string) {
	t.Helper()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	var out, errb bytes.Buffer
	code := mainWith(deps, args, strings.NewReader(""), &out, &errb)
	var env map[string]any
	if out.Len() > 0 {
		v, err := jsonx.Decode(out.Bytes())
		if err != nil {
			t.Fatalf("stdout %q: %v", out.String(), err)
		}
		m := map[string]any{}
		if o, ok := v.(*jsonx.Object); ok {
			for _, k := range o.Keys() {
				m[k], _ = o.Get(k)
			}
		}
		env = m
	}
	return code, env, errb.String()
}

const issuesConfig = `{"backlog": {"backend": "issues"}}`

func issueFixture() *trackertest.Fake {
	return &trackertest.Fake{Issues: []backlog.Issue{
		{Number: 7, Title: "Add export", Body: "Export the backlog.\n\n<!-- rota:fields\nRelated: B9\n-->",
			Labels: []string{"type:feature", "size:Major"}, Milestone: "M02 — Sharing", State: "open",
			URL: "https://example.test/issues/7"},
		{Number: 9, Title: "Crash on start", Labels: []string{"type:bug", "p1"}, State: "closed",
			StateReason: "not_planned", ClosedAt: "2026-09-30T10:00:00Z", URL: "https://example.test/issues/9"},
		{Number: 3, Title: "M02 tracking", Labels: []string{"milestone-tracker"}, State: "open"},
	}}
}

func issueData(t *testing.T, env map[string]any) *jsonx.Object {
	t.Helper()
	d, ok := env["data"].(*jsonx.Object)
	if !ok {
		t.Fatalf("no data in %v", env)
	}
	return d
}

func issueGet(o *jsonx.Object, k string) any { v, _ := o.Get(k); return v }

// Issue mode: every accepted spelling of an issue resolves to the canonical
// id (the number) with the type letter beside it (contract rule 11).
func TestIssueFieldGetCanonicalID(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	deps := withTracker(t, issueFixture())
	for _, ref := range []string{"7", "#7", "F7", "f7"} {
		code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "item", "field", "get", ref, "--name", "title")
		if code != 0 {
			t.Fatalf("%s: exit %d: %s", ref, code, stderr)
		}
		d := issueData(t, env)
		if issueGet(d, "id") != "7" || issueGet(d, "type") != "F" || issueGet(d, "value") != "Add export" {
			t.Fatalf("%s: data %v", ref, d)
		}
	}
	code, env, _ := rotaRunWith(t, deps, "--json", "-C", root, "item", "field", "get", "7", "--name", "milestone")
	if d := issueData(t, env); code != 0 || issueGet(d, "value") != "M02" {
		t.Fatalf("milestone: exit %d, %v", code, d)
	}
}

func TestIssueFieldListClosedItem(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	deps := withTracker(t, issueFixture())
	code, env, stderr := rotaRunWith(t, deps, "--json", "-C", root, "item", "field", "list", "#9")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	d := issueData(t, env)
	f, _ := issueGet(d, "fields").(*jsonx.Object)
	if issueGet(d, "id") != "9" || issueGet(d, "type") != "B" || f == nil ||
		issueGet(f, "reason") != "dropped" || issueGet(f, "detail") != "https://example.test/issues/9" {
		t.Fatalf("data %v", d)
	}
}

// Unknown numbers, a type-letter mismatch and milestone tracking issues are
// not items: exit 3.
func TestIssueRefsThatDoNotResolve(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	deps := withTracker(t, issueFixture())
	for _, ref := range []string{"99", "B7", "#3", "x7"} {
		if code, _, _ := rotaRunWith(t, deps, "--json", "-C", root, "item", "field", "get", ref, "--name", "title"); code != ExitResolution {
			t.Fatalf("%s: exit %d, want %d", ref, code, ExitResolution)
		}
	}
}

// File-only verbs are refused under issues (4, backend) before the tracker is built.
func TestIssueModeFileOnly(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	deps := withTracker(t, issueFixture())
	raw := filepath.Join(t.TempDir(), "bullet.md")
	if err := os.WriteFile(raw, []byte("- **[B05] [P1] Raw.** x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"id", "next", "--kind", "bugs"},
		{"item", "rm", "7"},
		{"item", "create", "--kind", "bugs", "--raw-file", raw},
		{"item", "field", "set", "7", "--name", "detail", "--value", "x"},
	} {
		code, env, _ := rotaRunWith(t, deps, append([]string{"--json", "-C", root}, argv...)...)
		if code != ExitRefused {
			t.Fatalf("%v: exit %d, want %d (%v)", argv, code, ExitRefused, env)
		}
	}
	// the milestones counter is refused too, and the refusal names the backend
	code, env, _ := rotaRunWith(t, deps, "--json", "-C", root, "id", "next", "--kind", "milestones")
	if d := ddata(t, env); code != ExitRefused || d["blockedBy"] != "backend" {
		t.Fatalf("id next --kind milestones: exit %d data %v", code, d)
	}
}

// Without an injected tracker the real one is built; a project with no
// origin remote has no provider to pick, which is exit 5.
func TestIssueModeWithoutTracker(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	if code, _, _ := rotaRun(t, "--json", "-C", root, "item", "field", "get", "7", "--name", "title"); code != ExitUnavailable {
		t.Fatalf("exit %d, want %d", code, ExitUnavailable)
	}
}
