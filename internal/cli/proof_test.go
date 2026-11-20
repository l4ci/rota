package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/l4ci/rota/internal/jsonx"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func backlogProject(t *testing.T) string {
	dir := gitRepo(t)
	os.WriteFile(filepath.Join(dir, ".rota/BACKLOG.md"), []byte(`# Backlog

## Bugs

- **[B07] [Major] Parser drops `+"`foo`"+`.** See `+"`parse()`"+`, unclear why? TBD.
- **[B08] [P1] Small.** Minor.
`), 0o644)
	return dir
}

func TestProofVerbs(t *testing.T) {
	dir := backlogProject(t)
	code, out, _ := rotaIn(t, dir, "proof", "add", "B07", "--check", "unit tests", "--result", "PASS", "--evidence", "ok", "--sha", "abc", "--json")
	d := data(t, out)
	if code != 0 || d["id"] != "B07" || d["type"] != "B" || d["check"] != "unit tests" || d["result"] != "PASS" || d["sha"] != "abc" || d["changed"] != true {
		t.Fatalf("add: %d %s", code, out)
	}
	if _, out, _ = rotaIn(t, dir, "proof", "add", "B07", "--check", "unit tests", "--result", "PASS", "--evidence", "ok", "--sha", "abc", "--json"); data(t, out)["changed"] != false {
		t.Errorf("repeat add: %s", out)
	}
	for _, args := range [][]string{
		{"proof", "add", "B07", "--check", "c", "--result", "pass", "--evidence", "e"},
		{"proof", "add", "B07", "--check", "c", "--result", "PASS"},
		{"proof", "add", "B07", "--result", "PASS", "--evidence", "e"},
		{"proof", "add", "nope", "--check", "c", "--result", "PASS", "--evidence", "e"},
		{"proof", "show", "nope"},
	} {
		if code, _, _ := rotaIn(t, dir, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code, _, _ := rotaIn(t, dir, "proof", "add", "B99", "--check", "c", "--result", "PASS", "--evidence", "e"); code != 3 {
		t.Errorf("unknown item: exit %d, want 3", code)
	}
	_, out, _ = rotaIn(t, dir, "proof", "show", "B07", "--json")
	d = data(t, out)
	rows, _ := d["rows"].([]any)
	if d["count"] != float64(1) || len(rows) != 1 || rows[0].(map[string]any)["check"] != "unit tests" {
		t.Errorf("show: %s", out)
	}
	if _, out, _ = rotaIn(t, dir, "proof", "show", "B07", "--count"); out != "1\n" {
		t.Errorf("--count text: %q", out)
	}
	if _, out, _ = rotaIn(t, dir, "proof", "show", "B07", "--count", "--json"); data(t, out)["rows"] == nil {
		t.Errorf("--json ignores --count: %s", out)
	}
	if code, out, _ := rotaIn(t, dir, "proof", "show", "B08"); code != 0 || out != "" {
		t.Errorf("no proof: %d %q", code, out)
	}
	_, out, _ = rotaIn(t, dir, "proof", "show", "B08", "--json")
	if data(t, out)["count"] != float64(0) {
		t.Errorf("no proof json: %s", out)
	}
}

func TestPlanUncertain(t *testing.T) {
	dir := backlogProject(t)
	code, out, _ := rotaIn(t, dir, "plan", "uncertain", "B07", "--json")
	d := data(t, out)
	if code != 0 || d["uncertain"] != true || d["type"] != "B" || len(d["reasons"].([]any)) != 2 {
		t.Fatalf("uncertain: %d %s", code, out)
	}
	code, out, _ = rotaIn(t, dir, "plan", "uncertain", "B08", "--json")
	if code != 1 || data(t, out)["uncertain"] != false {
		t.Errorf("certain: %d %s", code, out)
	}
	if code, _, _ := rotaIn(t, dir, "plan", "uncertain", "B99"); code != 3 {
		t.Errorf("unknown: %d", code)
	}
	os.WriteFile(filepath.Join(dir, ".rota/config.json"), []byte(`{"issues": {"source": "bogus"}}`), 0o644)
	if code, _, _ := rotaIn(t, dir, "plan", "uncertain", "B07"); code != 70 {
		t.Errorf("invalid backend: %d, want 70", code)
	}
}

func TestProofRecord(t *testing.T) {
	dir := backlogProject(t)
	code, out, _ := rotaIn(t, dir, "proof", "record", "B07", "--json", "--", "echo hi")
	d := data(t, out)
	if code != 0 || d["result"] != "PASS" || d["check"] != "echo hi" || d["exitCode"] != float64(0) || d["changed"] != true {
		t.Fatalf("passing: %d %s", code, out)
	}
	// sha256 of "hi\n"
	if ev, _ := d["evidence"].(string); !strings.HasPrefix(ev, "exit=0 output-sha256=98ea6e4f216f2fb4b69fff9b3a44842c38686ca685f3f55dc48c5d3fb1107be4") {
		t.Errorf("evidence: %q", ev)
	}
	code, out, _ = rotaIn(t, dir, "proof", "record", "B07", "--json", "--", "echo", "out; exit 3")
	if code != 0 || data(t, out)["check"] != "'echo' 'out; exit 3'" {
		t.Errorf("multi-arg quoting: %d %s", code, out)
	}
	code, out, _ = rotaIn(t, dir, "proof", "record", "B07", "--json", "--", "sh -c 'exit 3'")
	d = data(t, out)
	if code != 1 || d["result"] != "FAIL" || d["exitCode"] != float64(3) || d["changed"] != true {
		t.Fatalf("failing: %d %s", code, out)
	}
	if _, out, _ = rotaIn(t, dir, "proof", "record", "B07", "--json", "--", "echo hi"); data(t, out)["changed"] != false {
		t.Errorf("identical re-run duplicated the row: %s", out)
	}
	_, out, _ = rotaIn(t, dir, "proof", "show", "B07", "--json")
	if data(t, out)["count"] != float64(3) {
		t.Errorf("rows: %s", out)
	}
	for _, args := range [][]string{
		{"proof", "record", "B07"},
		{"proof", "record", "B07", "--"},
		{"proof", "record", "B07", "echo", "hi"},
		{"proof", "record", "--", "echo", "hi"},
	} {
		if code, _, _ := rotaIn(t, dir, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code, _, _ := rotaIn(t, dir, "proof", "record", "B99", "--", "touch ran.txt"); code != 3 {
		t.Errorf("unknown item: exit %d, want 3", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "ran.txt")); err == nil {
		t.Errorf("command ran for an unknown item")
	}
}

// failingProofStore is a store whose save fails, as a tracker write does on a
// quota error.
type failingProofStore struct{}

func (failingProofStore) Update(string, func(string) (string, bool, error)) error {
	return errors.New("graphql: rate limit")
}
func (failingProofStore) Read(string) (string, bool, error) { return "", false, nil }

func proofLogs(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, ".rota", "tmp", "*.log"))
	return m
}

func TestProofRecordFailWritesLog(t *testing.T) {
	dir := backlogProject(t)
	script := "i=1; while [ $i -le 30 ]; do echo line$i; i=$((i+1)); done; exit 3"
	code, out, _ := rotaIn(t, dir, "proof", "record", "B07", "--json", "--", script)
	d := data(t, out)
	if code != 1 || d["result"] != "FAIL" {
		t.Fatalf("failing: %d %s", code, out)
	}
	lp, _ := d["logPath"].(string)
	if lp == "" {
		t.Fatalf("no logPath: %s", out)
	}
	if !strings.HasPrefix(lp, filepath.Join(dir, ".rota", "tmp")) {
		t.Errorf("log outside the project temp root: %s", lp)
	}
	b, err := os.ReadFile(lp)
	if err != nil || !strings.Contains(string(b), "line1\n") || !strings.Contains(string(b), "line30") {
		t.Errorf("log lacks the full output: %v %q", err, b)
	}
	if ev, _ := d["evidence"].(string); strings.Contains(ev, lp) {
		t.Errorf("log path stored in the row evidence: %q", ev)
	}
	_, text, _ := rotaIn(t, dir, "proof", "record", "B08", "--", script)
	if !strings.Contains(text, "log: ") {
		t.Errorf("text lacks log line: %q", text)
	}
}

func TestProofRecordPassWritesNoLog(t *testing.T) {
	dir := backlogProject(t)
	code, out, _ := rotaIn(t, dir, "proof", "record", "B07", "--json", "--", "echo hi")
	d := data(t, out)
	if code != 0 {
		t.Fatalf("passing: %d %s", code, out)
	}
	if _, has := d["logPath"]; has {
		t.Errorf("PASS carries logPath: %s", out)
	}
	if logs := proofLogs(t, dir); len(logs) != 0 {
		t.Errorf("PASS wrote a log: %v", logs)
	}
}

func TestProofRecordSaveFailureKeepsOutcome(t *testing.T) {
	dir := backlogProject(t)
	c := &Ctx{Deps: depsIn(dir)}
	w := &who{ID: "B07", Type: "bug"}
	for _, tc := range []struct {
		code    int
		wantLog bool
	}{{3, true}, {0, false}} {
		run := proofRun{out: "boom\n", cwd: dir, code: tc.code}
		res, err := finishProofRecord(c, context.Background(), dir, failingProofStore{}, w, "make test", run)
		if err == nil {
			t.Fatalf("exit %d: save failure returned no error", tc.code)
		}
		want := fmt.Sprintf("exit %d", tc.code)
		if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "rate limit") {
			t.Errorf("exit %d: message %q lacks %q or the cause", tc.code, err, want)
		}
		if got := strings.Contains(err.Error(), "log: "); got != tc.wantLog {
			t.Errorf("exit %d: log in message = %v, want %v: %q", tc.code, got, tc.wantLog, err)
		}
		d, _ := res.Data.(*jsonx.Object)
		if d == nil {
			t.Fatalf("exit %d: no data", tc.code)
		}
		if v, _ := d.Get("exitCode"); v != tc.code {
			t.Errorf("exit %d: data exitCode = %v", tc.code, v)
		}
	}
}
