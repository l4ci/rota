package cli

import (
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
)

// planCheckRun sets item 7 up with a plan and, when design is not empty, a
// design note, then runs plan check on it. The fixture body of item 7 has no
// `## Acceptance` section.
func planCheckRun(t *testing.T, design, tasks string) (int, map[string]any, string) {
	t.Helper()
	root := trackerProject(t, issuesConfig)
	deps := withTracker(t, issueFixture())
	run := func(args ...string) {
		t.Helper()
		if code, env, stderr := issueRunWith(t, deps, root, args...); code != 0 {
			t.Fatalf("%v: %d %v %s", args, code, env, stderr)
		}
	}
	if design != "" {
		run("design", "add", "7", "--title", "Export design")
		run("design", "put", "7", "--body-file", bodyFile(t, design))
	}
	run("plan", "add", "#7", "--title", "Export plan")
	run("plan", "put", "#7", "--body-file", bodyFile(t, "## Goal\n\ng\n\n## Tasks\n\n"+tasks))
	code, env, stderr := issueRunWith(t, deps, root, "plan", "check", "#7")
	msg := stderr
	if e, ok := env["error"].(*jsonx.Object); ok {
		m, _ := e.Get("message")
		s, _ := m.(string)
		msg += s
	}
	return code, ddata(t, env), msg
}

const planCheckTask = "- **T1** — parse\n  - Serves: AC-1\n  - Verify: go test ./parse\n"

func TestPlanCheckReadsAcceptanceFromDesign(t *testing.T) {
	code, d, msg := planCheckRun(t, "# d\n\n## Acceptance\n\n- [ ] AC-1: it parses\n", planCheckTask)
	if code != 0 || d["ok"] != true || strings.Contains(msg, "serves unknown") {
		t.Fatalf("design ids: exit %d data %v %s", code, d, msg)
	}
}

func TestPlanCheckNoAcceptanceAnywhere(t *testing.T) {
	for name, design := range map[string]string{"no design": "", "design without ids": "# d\n"} {
		for _, tasks := range []string{planCheckTask, ""} {
			code, d, msg := planCheckRun(t, design, tasks)
			if code != 1 || d["ok"] != false || !strings.Contains(msg, "issue body") || !strings.Contains(msg, "design note") {
				t.Errorf("%s, tasks %q: exit %d data %v message %q", name, tasks, code, d, msg)
			}
		}
	}
}

func TestPlanCheckNamesTaskShape(t *testing.T) {
	code, _, msg := planCheckRun(t, "## Acceptance\n\n- [ ] AC-1: it parses\n",
		"### T1: parse\n\nServes: AC-1\nVerify: go test ./parse\n")
	for _, want := range []string{"- **T1**", "Serves:", "Verify:"} {
		if code != 1 || !strings.Contains(msg, want) {
			t.Fatalf("heading tasks: exit %d, message %q lacks %q", code, msg, want)
		}
	}
	code, env, _ := issueRun(t, t.TempDir(), "plan", "check", "--help")
	help, _ := ddata(t, env)["details"].(string)
	if code != 0 || !strings.Contains(help, "- **T1**") || !strings.Contains(help, "Serves:") || !strings.Contains(help, "Verify:") {
		t.Fatalf("help: exit %d details %q", code, help)
	}
}
