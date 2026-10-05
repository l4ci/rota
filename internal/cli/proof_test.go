package cli

import (
	"os"
	"path/filepath"
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
	os.WriteFile(filepath.Join(dir, ".rota/config.json"), []byte(`{"backlog": {"backend": "bogus"}}`), 0o644)
	if code, _, _ := rotaIn(t, dir, "plan", "uncertain", "B07"); code != 70 {
		t.Errorf("invalid backend: %d, want 70", code)
	}
}
