package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// verdictRepo is a git repo on feat/x with a main base branch and .rota/.
func verdictRepo(t *testing.T) string {
	t.Helper()
	dir := gitRepo(t)
	gitIn(t, dir, "branch", "main")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.email=a@b", "-c", "user.name=n"}, args...)...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func writeBody(t *testing.T, dir, text string) string {
	t.Helper()
	p := filepath.Join(dir, "body.json")
	if err := os.WriteFile(p, []byte(text), 0o666); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestVerdictAddValidates(t *testing.T) {
	dir := verdictRepo(t)
	bad := writeBody(t, dir, `{"findings": [{"severity": "huge", "title": "t"}]}`)
	cases := []struct {
		name string
		args []string
		want int
	}{
		{"no kind", []string{"--verdict", "PASS"}, 2},
		{"unknown kind", []string{"--kind", "lint", "--verdict", "PASS"}, 2},
		{"debug kind", []string{"--kind", "debug-fix", "--verdict", "PASS"}, 2},
		{"no verdict", []string{"--kind", "qa"}, 2},
		{"wrong verdict for kind", []string{"--kind", "review-spec", "--verdict", "INFRA-FAIL"}, 2},
		{"lowercase", []string{"--kind", "qa", "--verdict", "pass"}, 2},
		{"bad body", []string{"--kind", "qa", "--verdict", "FAIL", "--body-file", bad}, 2},
		{"missing body file", []string{"--kind", "qa", "--verdict", "FAIL", "--body-file", filepath.Join(dir, "nope")}, 2},
		{"body verdict differs", []string{"--kind", "qa", "--verdict", "FAIL", "--body-file", writeBody(t, t.TempDir(), `{"verdict": "PASS"}`)}, 2},
		{"unknown branch", []string{"nope", "--kind", "qa", "--verdict", "FAIL"}, 3},
	}
	for _, c := range cases {
		code, _, _ := rotaIn(t, dir, append([]string{"verdict", "add"}, c.args...)...)
		if code != c.want {
			t.Errorf("%s: exit %d, want %d", c.name, code, c.want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "verdicts.json")); err == nil {
		t.Error("a rejected verdict wrote the store")
	}
}

func TestVerdictReviewFlow(t *testing.T) {
	dir := verdictRepo(t)
	body := writeBody(t, dir, `{"summary": "s", "findings": [{"severity": "major", "title": "naming", "file": "a.go", "line": 4}]}`)
	code, out, _ := rotaIn(t, dir, "verdict", "add", "--kind", "review-spec", "--verdict", "CONCERNS", "--json")
	if d := data(t, out); code != 0 || d["next"] != "quality" || d["branch"] != "feat/x" || d["changed"] != true {
		t.Fatalf("spec: %d %v", code, d)
	}
	code, out, _ = rotaIn(t, dir, "verdict", "add", "--kind", "review-quality", "--verdict", "PASS", "--body-file", body, "--json")
	d := data(t, out)
	if code != 0 || d["combined"] != "CONCERNS" || d["next"] != "report" || d["summary"] != "s" {
		t.Fatalf("quality: %d %v", code, d)
	}
	if f := d["findings"].([]any)[0].(map[string]any); f["line"] != float64(4) || f["file"] != "a.go" {
		t.Errorf("findings: %v", f)
	}
	code, out, _ = rotaIn(t, dir, "verdict", "route", "--for", "ship-review", "--json")
	if d := data(t, out); code != 0 || d["verdict"] != "CONCERNS" || d["next"] != "ask" || d["stale"] != false || d["kind"] != "review-quality" {
		t.Fatalf("route: %d %v", code, d)
	}
	// Loop mode addresses the concerns instead of asking.
	if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"autonomy": {"level": "loop"}}`), 0o666); err != nil {
		t.Fatal(err)
	}
	if _, out, _ = rotaIn(t, dir, "verdict", "route", "--for", "ship-review", "--json"); data(t, out)["next"] != "address" {
		t.Errorf("loop route: %v", data(t, out))
	}
	// A new commit makes the verdict stale without changing the route.
	gitIn(t, dir, "commit", "-q", "--allow-empty", "-m", "more")
	_, out, _ = rotaIn(t, dir, "verdict", "route", "--for", "ship-review", "--json")
	if d := data(t, out); d["stale"] != true || d["next"] != "address" {
		t.Errorf("stale route: %v", d)
	}
	_, out, _ = rotaIn(t, dir, "verdict", "show", "--json")
	if d := data(t, out); len(d["records"].([]any)) != 2 || d["head"] == "" {
		t.Errorf("show: %v", d)
	}
}

func TestVerdictRouteMissingAndUsage(t *testing.T) {
	dir := verdictRepo(t)
	if code, _, _ := rotaIn(t, dir, "verdict", "route", "--for", "ship-qa"); code != 3 {
		t.Errorf("no verdict: exit %d, want 3", code)
	}
	if code, _, _ := rotaIn(t, dir, "verdict", "route"); code != 2 {
		t.Errorf("no --for: exit %d, want 2", code)
	}
	if code, _, _ := rotaIn(t, dir, "verdict", "route", "--for", "ship"); code != 2 {
		t.Errorf("unknown --for: exit %d, want 2", code)
	}
	code, out, _ := rotaIn(t, dir, "verdict", "show", "--json")
	if d := data(t, out); code != 0 || len(d["records"].([]any)) != 0 {
		t.Errorf("empty show: %d %v", code, d)
	}
	// A verdict on another branch is not this branch's.
	gitIn(t, dir, "branch", "other")
	rotaIn(t, dir, "verdict", "add", "other", "--kind", "qa", "--verdict", "FAIL")
	if code, _, _ := rotaIn(t, dir, "verdict", "route", "--for", "ship-qa"); code != 3 {
		t.Errorf("other branch's verdict was read: exit %d", code)
	}
	if _, out, _ := rotaIn(t, dir, "verdict", "route", "other", "--for", "ship-qa", "--json"); data(t, out)["next"] != "surface" {
		t.Errorf("advisory qa route: %v", data(t, out))
	}
}

func TestDebugVerdictIronLaw(t *testing.T) {
	dir := verdictRepo(t)
	if code, _, _ := rotaIn(t, dir, "debug", "verdict", "B07", "--verdict", "CONCERNS"); code != 2 {
		t.Errorf("CONCERNS: exit %d, want 2", code)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "verdict", "B07"); code != 2 {
		t.Errorf("no verdict: exit %d, want 2", code)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "verdict", " ", "--verdict", "FAIL"); code != 2 {
		t.Errorf("blank id: exit %d, want 2", code)
	}
	rotaIn(t, dir, "debug", "counter", "init", "B07")
	rotaIn(t, dir, "debug", "counter", "record-attempt", "--hypothesis", "h1", "--commit", "abc")
	want := []string{"hypothesize", "hypothesize", "halt"}
	for i, next := range want {
		code, out, _ := rotaIn(t, dir, "debug", "verdict", "B07", "--verdict", "FAIL", "--json")
		d := data(t, out)
		if code != 0 || d["next"] != next || d["failedFixes"] != float64(i+1) || d["bugId"] != "B07" || d["kind"] != "debug-fix" {
			t.Fatalf("fail %d: %d %v", i+1, code, d)
		}
		// Only the first call had a pending counter attempt to close.
		if _, has := d["attempt"]; has != (i == 0) {
			t.Errorf("fail %d: attempt present=%v", i+1, has)
		}
	}
	_, out, _ := rotaIn(t, dir, "debug", "counter", "show", "--json")
	if d := data(t, out); d["failedFixes"] != float64(1) {
		t.Errorf("counter not closed: %v", d)
	}
	// At 3 failed fixes the Iron Law refuses a 4th attempt until a reset (B3).
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "record-attempt", "--hypothesis", "h2", "--commit", "def"); code != 4 {
		t.Fatalf("4th attempt: exit %d, want 4", code)
	}
	rotaIn(t, dir, "debug", "reset", "B07", "--reason", "new angle", "--confirm", "--confirm-note", "yes")
	rotaIn(t, dir, "debug", "counter", "record-attempt", "--hypothesis", "h2", "--commit", "def")
	code, out, _ := rotaIn(t, dir, "debug", "verdict", "B07", "--verdict", "PASS", "--json")
	if d := data(t, out); code != 0 || d["next"] != "complete" || d["attempt"] != float64(2) || d["failedFixes"] != float64(0) {
		t.Fatalf("pass: %d %v", code, d)
	}
}
