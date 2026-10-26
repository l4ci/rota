package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/verdict"
)

// ---- /rota-work Step 7 fix-attempt cap

func TestWorkFixAttemptCapsAtThree(t *testing.T) {
	dir := verdictRepo(t)
	for n := 1; n <= 3; n++ {
		code, out, _ := rotaIn(t, dir, "debug", "work-fix", "attempt", "B07", "--json")
		d := data(t, out)
		if code != 0 || d["attempt"] != float64(n) || d["changed"] != true {
			t.Fatalf("attempt %d: exit %d data %v", n, code, d)
		}
	}
	code, out, _ := rotaIn(t, dir, "debug", "work-fix", "attempt", "B07", "--json")
	d := data(t, out)
	if code != 4 || d["blockedBy"] != "iron law" || d["bugId"] != "B07" || d["failedAttempts"] != float64(3) ||
		d["next"] != "/rota-debug B07" || d["changed"] != false {
		t.Fatalf("fourth attempt: exit %d data %v", code, d)
	}
	if !strings.Contains(out, "rota debug work-fix reset B07") {
		t.Errorf("hint missing: %s", out)
	}
	// The refusal recorded nothing, and another item is unaffected.
	if n := len(verdict.Load(dir).Items["B07"]); n != 3 {
		t.Errorf("a refused attempt recorded: %d records", n)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "work-fix", "attempt", "B08"); code != 0 {
		t.Errorf("B08: exit %d", code)
	}
	// The work-fix count is not the debug Iron Law count: /rota-debug stays open.
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "init", "B07"); code != 0 {
		t.Errorf("debug counter init refused by the work-fix cap: exit %d", code)
	}
}

func TestWorkFixPassResetsCount(t *testing.T) {
	dir := verdictRepo(t)
	for i := 0; i < 2; i++ {
		rotaIn(t, dir, "debug", "work-fix", "attempt", "B07")
	}
	if code, _, _ := rotaIn(t, dir, "debug", "work-fix", "pass", "B07"); code != 0 {
		t.Fatalf("pass: exit %d", code)
	}
	for n := 1; n <= 3; n++ {
		code, out, _ := rotaIn(t, dir, "debug", "work-fix", "attempt", "B07", "--json")
		if d := data(t, out); code != 0 || d["attempt"] != float64(n) {
			t.Fatalf("attempt %d after pass: exit %d data %v", n, code, d)
		}
	}
	if code, _, _ := rotaIn(t, dir, "debug", "work-fix", "attempt", "B07"); code != 4 {
		t.Errorf("fourth attempt after pass: exit %d, want 4", code)
	}
}

func TestWorkFixReset(t *testing.T) {
	dir := verdictRepo(t)
	for _, c := range [][]string{{"B07"}, {"B07", "--reason", "  "}, {"B07", "--reason", "r", "--confirm"}} {
		if code, _, _ := rotaIn(t, dir, append([]string{"debug", "work-fix", "reset"}, c...)...); code != 2 {
			t.Errorf("%v: exit %d, want 2", c, code)
		}
	}
	for i := 0; i < 3; i++ {
		rotaIn(t, dir, "debug", "work-fix", "attempt", "B07")
	}
	// Without --confirm the manual gate refuses and the cap stands.
	code, out, _ := rotaIn(t, dir, "debug", "work-fix", "reset", "B07", "--reason", "new angle", "--json")
	if d := data(t, out); code != 4 || d["gate"] != "debug-reset" || d["changed"] != false {
		t.Fatalf("unconfirmed reset: %d %v", code, d)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "work-fix", "attempt", "B07"); code != 4 {
		t.Fatalf("attempt after refused reset: exit %d, want 4", code)
	}
	code, out, _ = rotaIn(t, dir, "debug", "work-fix", "reset", "B07", "--reason", "new angle", "--confirm", "--confirm-note", "yes, reset it", "--json")
	if d := data(t, out); code != 0 || d["cleared"] != float64(3) || d["changed"] != true {
		t.Fatalf("reset: %d %v", code, d)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "work-fix", "attempt", "B07"); code != 0 {
		t.Errorf("attempt after reset: exit %d", code)
	}
}

func TestWorkFixCountsPerTask(t *testing.T) {
	dir := verdictRepo(t)
	for i := 0; i < 3; i++ {
		if code, _, _ := rotaIn(t, dir, "debug", "work-fix", "attempt", "B07", "--task", "1"); code != 0 {
			t.Fatalf("task 1 attempt %d: exit %d", i+1, code)
		}
	}
	// A sibling task starts at zero, and its PASS leaves task 1's count alone.
	code, out, _ := rotaIn(t, dir, "debug", "work-fix", "attempt", "B07", "--task", "2", "--json")
	if d := data(t, out); code != 0 || d["attempt"] != float64(1) {
		t.Fatalf("task 2: exit %d data %v", code, d)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "work-fix", "pass", "B07", "--task", "2"); code != 0 {
		t.Fatalf("task 2 pass: exit %d", code)
	}
	code, out, _ = rotaIn(t, dir, "debug", "work-fix", "attempt", "B07", "--task", "1", "--json")
	if d := data(t, out); code != 4 || d["failedAttempts"] != float64(3) || d["next"] != "/rota-debug B07" {
		t.Fatalf("task 1 fourth attempt: exit %d data %v", code, d)
	}
	// Resetting task 2 (nothing to clear) leaves task 1 capped; resetting task 1 lifts only it.
	rotaIn(t, dir, "debug", "work-fix", "attempt", "B07", "--task", "2")
	code, _, _ = rotaIn(t, dir, "debug", "work-fix", "reset", "B07", "--task", "1", "--reason", "r", "--confirm", "--confirm-note", "ok")
	if code != 0 {
		t.Fatalf("task 1 reset: exit %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "work-fix", "attempt", "B07", "--task", "1"); code != 0 {
		t.Errorf("task 1 after reset: exit %d", code)
	}
	if code, out, _ := rotaIn(t, dir, "debug", "work-fix", "attempt", "B07", "--task", "2", "--json"); code != 0 || data(t, out)["attempt"] != float64(2) {
		t.Errorf("task 2 count disturbed by task 1 reset: %d %s", code, out)
	}
}

// On an issue backend debugItem canonicalizes every stored item key; a task's
// count must survive that and stay apart from its siblings.
func TestWorkFixCapOnIssueBackend(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	deps := withTracker(t, issueFixture())
	run := func(args ...string) (int, map[string]any) {
		code, env, _ := rotaRunWith(t, deps, append([]string{"--json", "-C", root, "debug", "work-fix"}, args...)...)
		return code, ddata(t, env)
	}
	for n := 1; n <= 3; n++ {
		if code, d := run("attempt", "7", "--task", "1"); code != 0 || fmt.Sprint(d["attempt"]) != fmt.Sprint(n) {
			t.Fatalf("attempt %d: exit %d data %v", n, code, d)
		}
	}
	if code, d := run("attempt", "7", "--task", "1"); code != 4 || d["blockedBy"] != "iron law" {
		t.Fatalf("fourth attempt: exit %d data %v", code, d)
	}
	if code, _ := run("attempt", "7", "--task", "2"); code != 0 {
		t.Fatalf("sibling task refused: exit %d", code)
	}
	if code, _ := run("pass", "7", "--task", "2"); code != 0 {
		t.Fatalf("pass: exit %d", code)
	}
	if code, _ := run("attempt", "7", "--task", "1"); code != 4 {
		t.Fatalf("sibling PASS cleared task 1: exit %d", code)
	}
	if code, d := run("reset", "7", "--task", "1", "--reason", "r", "--confirm", "--confirm-note", "ok"); code != 0 || d["changed"] != true {
		t.Fatalf("reset: exit %d data %v", code, d)
	}
	if code, _ := run("attempt", "7", "--task", "1"); code != 0 {
		t.Fatalf("attempt after reset: exit %d", code)
	}
}
