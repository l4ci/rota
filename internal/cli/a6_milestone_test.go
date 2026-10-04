package cli

import (
	"strings"
	"testing"
)

func TestMilestoneVerbs(t *testing.T) {
	dir := gitRepo(t)
	for _, args := range [][]string{{"milestone", "add", "--title", "T"}, {"milestone", "add", "--summary", "S"}, {"milestone", "add", "x", "--title", "T", "--summary", "S"}} {
		if code, _, _ := rotaIn(t, dir, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	code, out, _ := rotaIn(t, dir, "milestone", "add", "--title", "First", "--summary", "S", "--json")
	if d := data(t, out); code != 0 || d["id"] != "M01" || d["changed"] != true {
		t.Fatalf("add: %d %s", code, out)
	}
	rotaIn(t, dir, "milestone", "add", "--title", "Second", "--summary", "S", "--depends", "M01")
	_, out, _ = rotaIn(t, dir, "milestone", "list", "--json")
	if !strings.Contains(out, `"ready": false`) || !strings.Contains(out, `"depends": ["M01"]`) {
		t.Errorf("list: %s", out)
	}
	_, out, _ = rotaIn(t, dir, "milestone", "status", "M01", "--to", "shipped", "--json")
	if d := data(t, out); d["status"] != "shipped" || d["changed"] != true {
		t.Errorf("status: %s", out)
	}
	rotaIn(t, dir, "milestone", "status", "M02", "--to", "active")
	if _, out, _ = rotaIn(t, dir, "milestone", "active", "--json"); !strings.Contains(out, `"ids": ["M02"]`) {
		t.Errorf("active: %s", out)
	}
	if code, out, _ := rotaIn(t, dir, "milestone", "active"); code != 0 || out != "M02\n" {
		t.Errorf("active text: %d %q", code, out)
	}
	if code, _, _ := rotaIn(t, dir, "milestone", "status", "M02"); code != 2 {
		t.Errorf("status without --to: %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "milestone", "status", "M09", "--to", "active"); code != 3 {
		t.Errorf("status unknown: %d", code)
	}
	if code, out, _ := rotaIn(t, dir, "milestone", "show", "M01"); code != 0 || !strings.HasPrefix(out, "---\nid: M01\ntitle: First\nstatus: shipped\n") {
		t.Errorf("show: %d %q", code, out)
	}
	if code, _, _ := rotaStdin(t, dir, "---\nid: M02\n---\nx\n", "milestone", "put", "M01", "--body-file", "-"); code != 4 {
		t.Errorf("put wrong id: %d, want 4", code)
	}
	_, out, _ = rotaStdin(t, dir, "---\nid: M01\nstatus: shipped\n---\nx\n", "milestone", "put", "M01", "--body-file", "-", "--json")
	if data(t, out)["changed"] != true {
		t.Errorf("put: %s", out)
	}
	if _, out, _ = rotaIn(t, dir, "milestone", "index", "--json"); data(t, out)["changed"] != false {
		t.Errorf("index of a fresh tree: %s", out)
	}
	if code, _, _ := rotaIn(t, dir, "milestone", "rm", "M01"); code != 2 {
		t.Errorf("no milestone rm: exit %d, want 2", code)
	}
}
