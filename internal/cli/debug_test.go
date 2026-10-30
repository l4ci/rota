package cli

import (
	"bytes"
	"encoding/json"
	"github.com/l4ci/rota/internal/gittest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rotaIn runs the real tree in dir and returns exit, stdout, stderr.
func rotaIn(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	var so, se bytes.Buffer
	code := mainWith(depsIn(dir), args, strings.NewReader(""), &so, &se)
	return code, so.String(), se.String()
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := gittest.TempDir(t)
	gittest.Init(t, dir, "feat/x")
	gittest.Run(t, dir, "commit", "-q", "--allow-empty", "-m", "i")
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o777)
	return dir
}

func data(t *testing.T, out string) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad envelope %q: %v", out, err)
	}
	return env.Data
}

func TestDebugCounterLifecycle(t *testing.T) {
	dir := gitRepo(t)
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "show"); code != 3 {
		t.Fatalf("show before init: exit %d, want 3", code)
	}
	code, out, _ := rotaIn(t, dir, "debug", "counter", "init", "B07", "--json")
	if d := data(t, out); code != 0 || d["session"] != "feat-x" || d["changed"] != true {
		t.Fatalf("init: %d %v", code, d)
	}
	if _, out, _ = rotaIn(t, dir, "debug", "counter", "init", "B07", "--json"); data(t, out)["changed"] != false {
		t.Error("second init must be a no-op")
	}
	for _, verb := range []string{"fail", "pass"} {
		code, out, _ := rotaIn(t, dir, "debug", "counter", verb, "--json")
		if d := data(t, out); code != 4 || d["blockedBy"] != "attempt" || d["changed"] != false {
			t.Errorf("%s with no attempt: exit %d data %v, want 4 with blockedBy attempt", verb, code, d)
		}
	}
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "summary", "--json"); code != 1 {
		t.Errorf("summary with no attempts: exit %d, want 1", code)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "record-attempt", "--hypothesis", "h"); code != 2 {
		t.Errorf("record-attempt without --commit: exit %d, want 2", code)
	}
	_, out, _ = rotaIn(t, dir, "debug", "counter", "record-attempt", "--hypothesis", "h1", "--commit", "abc", "--json")
	if data(t, out)["attempt"] != float64(1) {
		t.Errorf("attempt: %s", out)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "pass"); code != 0 {
		t.Errorf("pass: exit %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "pass"); code != 4 {
		t.Errorf("second pass: exit %d, want 4", code)
	}
	rotaIn(t, dir, "debug", "counter", "record-attempt", "--hypothesis", "h2", "--commit", "def")
	_, out, _ = rotaIn(t, dir, "debug", "counter", "fail", "--json")
	if data(t, out)["failedFixes"] != float64(1) {
		t.Errorf("fail: %s", out)
	}
	_, out, _ = rotaIn(t, dir, "debug", "counter", "inc-cycle", "--json")
	if data(t, out)["hypothesisCycles"] != float64(1) {
		t.Errorf("inc-cycle: %s", out)
	}
	code, out, _ = rotaIn(t, dir, "debug", "counter", "summary", "--json")
	md, _ := data(t, out)["markdown"].(string)
	if code != 0 || !strings.Contains(md, "1 fix attempt failed to resolve the bug") || !strings.Contains(md, "2. def — h2") {
		t.Errorf("summary: %d %q", code, md)
	}
	_, out, _ = rotaIn(t, dir, "debug", "counter", "show", "--json")
	atts := data(t, out)["attempts"].([]any)
	if len(atts) != 2 || atts[0].(map[string]any)["outcome"] != "passed" || atts[1].(map[string]any)["endedAt"] == nil {
		t.Errorf("show attempts: %v", atts)
	}
	if _, out, _ = rotaIn(t, dir, "debug", "counter", "clear", "--json"); data(t, out)["changed"] != true {
		t.Error("clear should report changed")
	}
	if _, out, _ = rotaIn(t, dir, "debug", "counter", "clear", "--json"); data(t, out)["changed"] != false {
		t.Error("second clear must be a no-op")
	}
}

func TestDebugCounterOutsideGit(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(dir, ".rota"), 0o777)
	if code, _, _ := rotaIn(t, dir, "debug", "counter", "init", "B01"); code != 5 {
		t.Errorf("exit %d, want 5", code)
	}
}

func TestSpikeLifecycle(t *testing.T) {
	dir := gitRepo(t)
	if code, _, _ := rotaIn(t, dir, "spike", "add", "Bad_Name", "--question", "q"); code != 2 {
		t.Errorf("bad name: exit %d, want 2", code)
	}
	if code, _, _ := rotaIn(t, dir, "spike", "add", "sse"); code != 2 {
		t.Errorf("missing --question: exit %d, want 2", code)
	}
	code, out, _ := rotaIn(t, dir, "spike", "add", "sse", "--question", "Can SSE work?", "--json")
	if d := data(t, out); code != 0 || d["branch"] != "spike/sse" || d["changed"] != true {
		t.Fatalf("add: %d %s", code, out)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".rota/spikes/sse.md"))
	if !strings.HasPrefix(string(raw), "---\nname: sse\nbranch: spike/sse\nstatus: open\ncreated: ") || !strings.Contains(string(raw), "## Question\n\nCan SSE work?\n") {
		t.Errorf("spike file:\n%s", raw)
	}
	code, out, _ = rotaIn(t, dir, "spike", "add", "sse", "--question", "again", "--json")
	if d := data(t, out); code != 4 || d["blockedBy"] != "exists" || d["changed"] != false {
		t.Errorf("duplicate: exit %d data %v, want 4 with blockedBy exists", code, d)
	}
	if _, out, _ = rotaIn(t, dir, "spike", "list", "--json"); !strings.Contains(out, `"branchExists": true`) || !strings.Contains(out, `"status": "open"`) {
		t.Errorf("list: %s", out)
	}
	if code, out, _ := rotaIn(t, dir, "spike", "show", "sse"); code != 0 || string(raw) != out {
		t.Errorf("show: %d, text differs from file", code)
	}
	if code, _, _ := rotaIn(t, dir, "spike", "show", "nope"); code != 3 {
		t.Errorf("show missing: exit %d, want 3", code)
	}
	if code, _, _ := rotaIn(t, dir, "spike", "show", "../x"); code != 2 {
		t.Errorf("show bad name: exit %d, want 2", code)
	}
	if _, out, _ = rotaIn(t, dir, "spike", "finish", "sse", "--json"); data(t, out)["changed"] != true {
		t.Errorf("finish: %s", out)
	}
	done, _ := os.ReadFile(filepath.Join(dir, ".rota/spikes/sse.md"))
	if !strings.Contains(string(done), "status: done\nfinished: ") {
		t.Errorf("finished date missing:\n%s", done)
	}
	if _, out, _ = rotaIn(t, dir, "spike", "finish", "sse", "--json"); data(t, out)["changed"] != false {
		t.Error("second finish must be a no-op")
	}
	if code, _, _ := rotaIn(t, dir, "spike", "finish", "nope"); code != 3 {
		t.Errorf("finish missing: exit %d, want 3", code)
	}
}

func TestSpikeAddBranchExistsLeavesNoFile(t *testing.T) {
	dir := gitRepo(t)
	gittest.Run(t, dir, "branch", "spike/dup")
	if code, _, _ := rotaIn(t, dir, "spike", "add", "dup", "--question", "q"); code != 4 {
		t.Errorf("exit %d, want 4", code)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota/spikes/dup.md")); err == nil {
		t.Error("spike file written despite existing branch")
	}
}

func TestDebugSessionLifecycle(t *testing.T) {
	dir := gitRepo(t)
	if code, _, _ := rotaIn(t, dir, "debug", "session", "show", "b07"); code != 3 {
		t.Fatalf("show unknown: exit %d, want 3", code)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "session", "rm", "b07"); code != 3 {
		t.Fatalf("rm unknown: exit %d, want 3", code)
	}
	for _, bad := range []string{"a/b", "..", "../x"} {
		if code, _, _ := rotaIn(t, dir, "debug", "session", "put", bad, "--body-file", bodyFile(t, "x")); code != 2 {
			t.Fatalf("put %q: exit %d, want 2", bad, code)
		}
	}
	code, out, _ := rotaIn(t, dir, "debug", "session", "put", "b07", "--body-file", bodyFile(t, "# Symptom\nfirst\n"), "--json")
	if d := data(t, out); code != 0 || d["slug"] != "b07" {
		t.Fatalf("put: %d %v", code, d)
	}
	rotaIn(t, dir, "debug", "session", "put", "b07", "--body-file", bodyFile(t, "# Symptom\nsecond\n"))
	code, out, _ = rotaIn(t, dir, "debug", "session", "show", "b07", "--json")
	if d := data(t, out); code != 0 || d["body"] != "# Symptom\nsecond\n" {
		t.Fatalf("show after replace: %d %v", code, d)
	}
	code, out, _ = rotaIn(t, dir, "debug", "session", "list", "--json")
	if d := data(t, out); code != 0 || len(d["sessions"].([]any)) != 1 {
		t.Fatalf("list: %d %v", code, d)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "session", "rm", "b07"); code != 0 {
		t.Fatalf("rm: exit %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "debug", "session", "show", "b07"); code != 3 {
		t.Fatalf("show after rm: exit %d, want 3", code)
	}
}
