package cli

import (
	"bytes"
	"github.com/l4ci/rota/internal/jsonx"
	"os"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/backlog/trackertest"
)

func lastBody(fake *trackertest.Fake) string { return fake.Issues[len(fake.Issues)-1].Body }

func TestIssueCreateNumbersAcceptance(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	fake := issueFixture()
	deps := withTracker(t, fake)
	body := "Why.\n\n## Acceptance\n\n- [ ] first\n- [ ] AC-4: kept\n- [ ] second\n"
	code, _, stderr := issueRunWith(t, deps, root, "item", "create", "--kind", "features", "--title", "T",
		"--tag", "Minor", "--body-file", bodyFile(t, body))
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	got := lastBody(fake)
	for _, want := range []string{"- [ ] AC-5: first", "- [ ] AC-4: kept", "- [ ] AC-6: second"} {
		if !strings.Contains(got, want) {
			t.Errorf("body lacks %q:\n%s", want, got)
		}
	}
}

const acceptBody = "Export.\n\n## Acceptance\n\n- [ ] first\n- [ ] second\n"

func acceptFixture() *trackertest.Fake {
	fake := issueFixture()
	fake.Issues[0].Body = acceptBody
	return fake
}

func addProof(t *testing.T, deps *Deps, root, result, sha, check string) {
	t.Helper()
	if code, _, stderr := issueRunWith(t, deps, root, "proof", "add", "7", "--check", check,
		"--result", result, "--evidence", "e", "--sha", sha); code != 0 {
		t.Fatalf("proof add: %d %s", code, stderr)
	}
}

func TestPlanPass(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	fake := acceptFixture()
	deps := withTracker(t, fake)
	pass := func(args ...string) (int, map[string]any) {
		t.Helper()
		code, env, _ := issueRunWith(t, deps, root, append([]string{"plan", "pass"}, args...)...)
		return code, env
	}

	// Refusals and usage errors leave the body alone.
	for _, c := range []struct {
		want int
		args []string
	}{
		{4, []string{"M02-F7", "AC-1", "--proof", "abc1234:go test: ./x"}}, // no proof rows
		{2, []string{"M02-F7", "AC-9", "--proof", "abc1234:go test: ./x"}}, // unknown criterion
		{2, []string{"M02-F7", "1", "--proof", "abc1234:go test: ./x"}},    // not an AC id
		{2, []string{"M02-F7", "AC-1", "--proof", "abc1234"}},              // no check
		{2, []string{"M02-F7", "AC-1", "--proof", ":check"}},               // no sha
		{2, []string{"M02-F7", "AC-1"}},                                    // no --proof
		{2, []string{"M02-S01", "AC-1", "--proof", "abc1234:c"}},           // slice key
		{2, []string{"nonsense", "AC-1", "--proof", "abc1234:c"}},          // bad key
		{3, []string{"M02-F99", "AC-1", "--proof", "abc1234:c"}},           // unknown item
	} {
		if code, _ := pass(c.args...); code != c.want {
			t.Errorf("%v: exit %d, want %d", c.args, code, c.want)
		}
	}
	if fake.Issues[0].Body != acceptBody {
		t.Fatalf("a refused pass edited the body:\n%s", fake.Issues[0].Body)
	}

	addProof(t, deps, root, "PASS", "abc1234", "go test: ./x")
	code, env := pass("M02-F7", "AC-2", "--proof", "abc1234:go test: ./x")
	d := ddata(t, env)
	if code != 0 || d["ac"] != "AC-2" || d["item"] != "7" || d["key"] != "M02-F7" || d["changed"] != true || d["proof"] != "abc1234:go test: ./x" {
		t.Fatalf("pass: %d %v", code, d)
	}
	if b := fake.Issues[0].Body; !strings.Contains(b, "- [ ] AC-1: first") || !strings.Contains(b, "- [ ] AC-2: second") {
		t.Errorf("legacy body not numbered:\n%s", b)
	}
	if strings.Contains(fake.Issues[0].Body, "[x]") {
		t.Error("pass must not tick the body")
	}
	_, env, _ = issueRunWith(t, deps, root, "item", "note", "show", "7", "--kind", "acceptance")
	if body, _ := ddata(t, env)["body"].(string); !strings.Contains(body, "- AC-2 · ") || !strings.Contains(body, " · abc1234 · go test: ./x · second") {
		t.Errorf("note: %q", body)
	}

	// Re-passing, by a longer sha and an item-only key, changes nothing.
	code, env = pass("#7", "AC-2", "--proof", "abc1234def:go test: ./x")
	if d := ddata(t, env); code != 0 || d["changed"] != false {
		t.Errorf("repeat: %d %v", code, d)
	}

	// The latest row wins: a FAIL after the PASS refuses a new pass.
	addProof(t, deps, root, "FAIL", "abc1234", "go test: ./x")
	if code, _ := pass("M02-F7", "AC-1", "--proof", "abc1234:go test: ./x"); code != 4 {
		t.Errorf("latest FAIL: exit %d, want 4", code)
	}

	// Only the verb writes the note.
	for _, verb := range []string{"add", "rm"} {
		args := []string{"item", "note", verb, "7", "--kind", "acceptance"}
		if verb == "add" {
			args = append(args, "--body-file", bodyFile(t, "- AC-1 · d · s · c · t\n"))
		}
		if code, _, _ := issueRunWith(t, deps, root, args...); code != 2 {
			t.Errorf("item note %s acceptance: exit %d, want 2", verb, code)
		}
	}
}

func TestPlanPassFileBackendRefused(t *testing.T) {
	root := trackerProject(t, "")
	code, env, _ := issueRunWith(t, testDeps(), root, "plan", "pass", "M02-B01", "AC-1", "--proof", "abc1234:c")
	if d := ddata(t, env); code != 4 || d["blockedBy"] != "backend" || d["changed"] != false {
		t.Fatalf("exit %d data %v", code, d)
	}
}

func TestItemShowAcceptance(t *testing.T) {
	root := trackerProject(t, issuesConfig)
	fake := acceptFixture()
	deps := withTracker(t, fake)
	crit := func() []any {
		t.Helper()
		code, env, stderr := issueRunWith(t, deps, root, "item", "show", "7")
		if code != 0 {
			t.Fatalf("show: %d %s", code, stderr)
		}
		list, _ := ddata(t, env)["acceptance"].([]any)
		return list
	}
	field := func(v any, k string) any { o := v.(*jsonx.Object); x, _ := o.Get(k); return x }

	// A legacy body shows the ids a first pass would give.
	list := crit()
	if len(list) != 2 || field(list[0], "id") != "AC-1" || field(list[1], "text") != "second" ||
		field(list[0], "met") != false || field(list[0], "proof") != nil || field(list[0], "flag") != nil {
		t.Fatalf("legacy: %v", list)
	}
	if code, env, _ := issueRunWith(t, deps, root, "item", "show", "9"); code != 0 {
		t.Fatalf("show 9: %d", code)
	} else if l, ok := ddata(t, env)["acceptance"].([]any); !ok || len(l) != 0 {
		t.Fatalf("no criteria: acceptance = %v", ddata(t, env)["acceptance"])
	}

	addProof(t, deps, root, "PASS", "abc1234", "go test")
	if code, _, stderr := issueRunWith(t, deps, root, "plan", "pass", "F7", "AC-2", "--proof", "abc1234:go test"); code != 0 {
		t.Fatalf("pass: %d %s", code, stderr)
	}
	list = crit()
	if field(list[1], "met") != true || field(list[1], "proof") != "abc1234:go test" || field(list[1], "flag") != nil {
		t.Fatalf("met: %v", list)
	}

	// Editing a met criterion's text is flagged on the next read.
	fake.Issues[0].Body = strings.Replace(fake.Issues[0].Body, "AC-2: second", "AC-2: second, reworded", 1)
	list = crit()
	if field(list[1], "met") != false || field(list[1], "flag") != "changed" {
		t.Fatalf("changed: %v", list)
	}
	// A FAIL recorded later makes the mark unproven; text mode names the flag.
	fake.Issues[0].Body = acceptBody2(fake.Issues[0].Body)
	addProof(t, deps, root, "FAIL", "abc1234", "go test")
	if list = crit(); field(list[1], "flag") != "unproven" {
		t.Fatalf("unproven: %v", list)
	}
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	var out, errb bytes.Buffer
	if code := mainWith(deps, []string{"-C", root, "item", "show", "7"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("text show: %d", code)
	}
	for _, want := range []string{"acceptance: 0/2 met", "AC-2", "unproven"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("text lacks %q:\n%s", want, out.String())
		}
	}
}

// acceptBody2 puts the second criterion's text back to what the mark saw.
func acceptBody2(body string) string {
	return strings.Replace(body, "AC-2: second, reworded", "AC-2: second", 1)
}
