package cli

import (
	"strings"
	"testing"
)

// One item whose criteria were written where /rota-plan puts them crosses plan
// check, the done gate, plan pass and item show, and all of them see AC-1.
func TestSectionCriteriaAcrossVerbs(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	fake := acceptFixture()
	fake.Issues[0].Body = "Export the backlog."
	deps := withTracker(t, fake)
	addProof(t, deps, root, "PASS", "abc1234", "check")
	run := func(want int, args ...string) (map[string]any, string) {
		t.Helper()
		code, env, stderr := issueRunWith(t, deps, root, args...)
		if code != want {
			t.Fatalf("%v: exit %d, want %d: %v %s", args, code, want, env, stderr)
		}
		return env, stderr
	}
	run(0, "item", "section", "set", "7", "--name", "Acceptance", "--body-file", bodyFile(t, "- [ ] it parses\n"))
	run(0, "plan", "add", "#7", "--title", "Export plan")
	run(0, "plan", "put", "#7", "--body-file", bodyFile(t, "## Goal\n\ng\n\n## Tasks\n\n"+planCheckTask))
	run(0, "plan", "check", "#7")
	if _, stderr := run(4, "item", "complete", "7", "--commit", "abc1234"); !strings.Contains(stderr, "AC-1") {
		t.Fatalf("refusal does not name AC-1: %s", stderr)
	}
	run(0, "plan", "pass", "#7", "AC-1", "--proof", "abc1234:check")
	env, _ := run(0, "item", "show", "7")
	if cover, _ := ddata(t, env)["acceptance"].([]any); len(cover) != 1 {
		t.Fatalf("coverage %v", ddata(t, env)["acceptance"])
	}
	run(0, "item", "complete", "7", "--commit", "abc1234")
}
