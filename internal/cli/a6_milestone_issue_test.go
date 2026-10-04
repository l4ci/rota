package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog"
	"github.com/l4ci/rota/internal/backlog/trackertest"
	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/tracker"
)

const msTrackerBody = "---\nid: M02\ntitle: Sharing\nstatus: planned\ndepends: [M01]\ncreated: 2026-09-01\n---\n\n# M02 — Sharing\n\n## Goal\n\nShare it.\n\n<!-- rota:fields\nDepends: M01\n-->"

// msFixture is a tracker with M01 shipped and M02 planned, each with a native milestone.
func msFixture() *trackertest.MS {
	return &trackertest.MS{
		Fake: &trackertest.Fake{Issues: []backlog.Issue{
			{Number: 7, Title: "Add export", Labels: []string{"type:feature", "size:Major"}, Milestone: "M02 — Sharing", State: "open"},
			{Number: 2, Title: "M01 — Core", Body: "---\nid: M01\ntitle: Core\nstatus: shipped\ndepends: []\n---\n\n# M01 — Core\n",
				Labels: []string{"milestone-tracker", "status:shipped"}, Milestone: "M01 — Core", State: "closed", StateReason: "completed"},
			{Number: 3, Title: "M02 — Sharing", Body: msTrackerBody, Labels: []string{"milestone-tracker", "status:planned"},
				Milestone: "M02 — Sharing", State: "open"},
		}},
		Native: []tracker.Milestone{{Number: 1, Title: "M01 — Core", State: "closed"}, {Number: 2, Title: "M02 — Sharing", State: "open"}},
	}
}

func TestIssueModeMilestones(t *testing.T) {
	root := a4Project(t, issuesConfig)
	fake := msFixture()
	withTracker(t, fake)

	code, env, stderr := issueRun(t, root, "milestone", "list")
	if code != 0 {
		t.Fatalf("list: %d %s", code, stderr)
	}
	rows, _ := ddata(t, env)["milestones"].([]any)
	if len(rows) != 2 {
		t.Fatalf("list rows: %v", rows)
	}
	r1, _ := rows[1].(*jsonx.Object)
	if r1 == nil || issueGet(r1, "id") != "M02" || issueGet(r1, "title") != "Sharing" || issueGet(r1, "status") != "planned" || issueGet(r1, "ready") != true {
		t.Errorf("list row M02: %v", rows[1])
	}

	_, env, _ = issueRun(t, root, "milestone", "add", "--title", "Third", "--summary", "S3.", "--depends", "M02")
	if d := ddata(t, env); d["id"] != "M03" || d["changed"] != true {
		t.Fatalf("add: %v", env)
	}
	var created *backlog.Issue
	for i := range fake.Issues {
		if fake.Issues[i].Title == "M03 — Third" {
			created = &fake.Issues[i]
		}
	}
	if created == nil || !strings.Contains(created.Labels[0], "milestone-tracker") || created.Labels[1] != "status:planned" ||
		!strings.Contains(created.Body, "<!-- rota:fields\nDepends: M02\n-->") || len(fake.Native) != 3 {
		t.Fatalf("created issue %+v, native %+v", created, fake.Native)
	}

	_, env, _ = issueRun(t, root, "milestone", "show", "M02")
	if body, _ := ddata(t, env)["body"].(string); !strings.HasPrefix(body, "---\nid: M02\n") || strings.Contains(body, "rota:fields") || !strings.HasSuffix(body, "\n") {
		t.Errorf("show: %q", body)
	}
	if code, _, _ := issueRun(t, root, "milestone", "show", "M09"); code != 3 {
		t.Errorf("show unknown: %d", code)
	}
	if code, _, _ := issueRun(t, root, "milestone", "show", "x"); code != 2 {
		t.Errorf("show malformed: %d", code)
	}

	code, env, _ = issueRun(t, root, "milestone", "put", "M02", "--body-file", bodyFile(t, "---\nid: M05\n---\nx\n"))
	if code != 4 {
		t.Errorf("put wrong id: %d, want 4", code)
	}
	_, env, _ = issueRun(t, root, "milestone", "put", "M02", "--body-file", bodyFile(t, "---\nid: M02\nstatus: shipped\ndepends: [M01, M09]\n---\n\n# M02\n"))
	if ddata(t, env)["changed"] != true {
		t.Errorf("put: %v", env)
	}
	if is := issueByNumber(fake, 3); !strings.Contains(is.Body, "status: planned") || !strings.Contains(is.Body, "Depends: M01, M09") {
		t.Errorf("status must follow the label, depends the frontmatter: %q", is.Body)
	}

	_, env, _ = issueRun(t, root, "milestone", "status", "M02", "--to", "active")
	if d := ddata(t, env); d["status"] != "active" || d["changed"] != true {
		t.Fatalf("status: %v", env)
	}
	if is := issueByNumber(fake, 3); !contains(is.Labels, "status:active") || contains(is.Labels, "status:planned") || !strings.Contains(is.Body, "status: active") {
		t.Errorf("labels/body after status: %v %q", is.Labels, is.Body)
	}
	_, env, _ = issueRun(t, root, "milestone", "status", "M02", "--to", "active")
	if ddata(t, env)["changed"] != false {
		t.Errorf("repeat status: %v", env)
	}
	issueRun(t, root, "milestone", "status", "M02", "--to", "shipped")
	if is := issueByNumber(fake, 3); is.State != "closed" || is.StateReason != "completed" || fake.Native[1].State != "closed" {
		t.Errorf("shipped: state %s/%s, native %s", is.State, is.StateReason, fake.Native[1].State)
	}
	issueRun(t, root, "milestone", "status", "M02", "--to", "active")
	if is := issueByNumber(fake, 3); is.State != "open" || fake.Native[1].State != "open" {
		t.Errorf("reopened: state %s, native %s", is.State, fake.Native[1].State)
	}
	if code, _, _ := issueRun(t, root, "milestone", "status", "M09", "--to", "active"); code != 3 {
		t.Errorf("status unknown: %d", code)
	}

	_, env, _ = issueRun(t, root, "milestone", "active")
	if ids, _ := ddata(t, env)["ids"].([]any); len(ids) != 1 || ids[0] != "M02" {
		t.Errorf("active: %v", env)
	}
	// status ran index: MILESTONES.md was seeded and the vision block written.
	ms, _ := os.ReadFile(filepath.Join(root, ".rota/MILESTONES.md"))
	if !strings.Contains(string(ms), "## Active milestones\n\n- M02 — Sharing\n") {
		t.Errorf("MILESTONES.md:\n%s", ms)
	}
	block, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if !strings.Contains(string(block), "the tracking issues (`rota milestone show MNN`)") || !strings.Contains(string(block), "- **M02** — Sharing (depends: M01, M09) ⚠ blocked") {
		t.Errorf("vision block:\n%s", block)
	}
	if _, env, _ = issueRun(t, root, "milestone", "index"); ddata(t, env)["changed"] != false {
		t.Errorf("index of an up-to-date tree: %v", env)
	}
}

func issueByNumber(f *trackertest.MS, n int) backlog.Issue {
	for _, is := range f.Issues {
		if is.Number == n {
			return is
		}
	}
	return backlog.Issue{}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestIssueModeSlicePlans(t *testing.T) {
	root := a4Project(t, issuesConfig)
	fake := msFixture()
	withTracker(t, fake)

	_, env, stderr := issueRun(t, root, "plan", "add", "--milestone", "M02", "--slice", "--title", "First", "--design", "F07", "--repos", "")
	_ = stderr
	if d := ddata(t, env); d["key"] != "M02-S01" || d["unitKind"] != "slice" {
		t.Fatalf("mint: %v", env)
	}
	_, env, _ = issueRun(t, root, "plan", "add", "--milestone", "M02", "--slice", "--title", "Second")
	if ddata(t, env)["key"] != "M02-S02" {
		t.Errorf("second mint: %v", env)
	}
	if code, _, _ := issueRun(t, root, "plan", "add", "M02-S01", "--title", "dup"); code != 4 {
		t.Errorf("duplicate explicit key: %d", code)
	}
	_, env, _ = issueRun(t, root, "plan", "add", "M02-S09", "--title", "Explicit")
	if ddata(t, env)["key"] != "M02-S09" {
		t.Errorf("explicit: %v", env)
	}
	_, env, _ = issueRun(t, root, "plan", "add", "--milestone", "M02", "--slice", "--title", "After nine")
	if ddata(t, env)["key"] != "M02-S10" {
		t.Errorf("mint after S09: %v", env)
	}
	if code, _, _ := issueRun(t, root, "plan", "add", "--milestone", "M07", "--slice", "--title", "x"); code != 3 {
		t.Errorf("milestone without tracker: %d, want 3", code)
	}

	_, env, _ = issueRun(t, root, "plan", "show", "M02-S01")
	body, _ := ddata(t, env)["body"].(string)
	if !strings.Contains(body, "unit: S01\nunitKind: slice\ndesign: note:F07:design\ntitle: First\n") {
		t.Errorf("show: %q", body)
	}
	_, env, _ = issueRun(t, root, "plan", "put", "M02-S01", "--body-file", bodyFile(t, "# replaced\n"))
	if ddata(t, env)["changed"] != true {
		t.Errorf("put: %v", env)
	}
	if code, _, _ := issueRun(t, root, "plan", "put", "M02-S77", "--body-file", bodyFile(t, "x")); code != 3 {
		t.Errorf("put missing: %d", code)
	}

	code, env, _ := issueRun(t, root, "plan", "list", "--milestone", "M02")
	plans, _ := ddata(t, env)["plans"].([]any)
	if code != 0 || len(plans) != 4 {
		t.Fatalf("list: %d %v", code, env)
	}
	if warns, _ := env["warnings"].([]any); len(warns) != 1 || !strings.Contains(warns[0].(string), "item plans live on their issues") {
		t.Errorf("warnings: %v", env["warnings"])
	}
	if code, _, _ := issueRun(t, root, "plan", "rm", "M02-S09"); code != 0 {
		t.Errorf("rm: %d", code)
	}
	if code, _, _ := issueRun(t, root, "plan", "rm", "M02-S09"); code != 3 {
		t.Errorf("second rm: %d", code)
	}
}
