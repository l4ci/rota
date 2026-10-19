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
	_, out, _ = rotaIn(t, dir, "milestone", "status", "M01", "--to", "shipped", "--force", "--json")
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

func TestMilestoneStatusShippedAudit(t *testing.T) {
	dir := trackerProject(t, "")
	rotaIn(t, dir, "milestone", "add", "--title", "First", "--summary", "S")
	rotaIn(t, dir, "milestone", "add", "--title", "Second", "--summary", "S")
	body := "---\nid: M01\ntitle: First\nstatus: planned\n---\n# M01\n\n## Acceptance\n\n- [x] AC-1: one\n- [ ] AC-2: two\n"
	if code, _, _ := rotaStdin(t, dir, body, "milestone", "put", "M01", "--body-file", "-"); code != 0 {
		t.Fatalf("put: %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "item", "create", "--kind", "bugs", "--title", "Open one", "--tag", "P2", "--milestone", "M01"); code != 0 {
		t.Fatalf("item create: %d", code)
	}
	code, out, errOut := rotaIn(t, dir, "milestone", "status", "M01", "--to", "shipped", "--json")
	d := data(t, out)
	if code != 4 || d["changed"] != false || d["unticked"] == nil || d["open"] == nil {
		t.Fatalf("refuse: %d %s", code, out)
	}
	if !strings.Contains(out, `"unticked": ["AC-2"]`) || !strings.Contains(errOut+out, "rota milestone check M01") || !strings.Contains(errOut+out, "--force") {
		t.Errorf("data or hint: %s | %s", out, errOut)
	}
	if _, out, _ = rotaIn(t, dir, "milestone", "show", "M01"); strings.Contains(out, "status: shipped") {
		t.Errorf("refused ship still changed status: %s", out)
	}
	for _, st := range []string{"active", "archived", "planned"} {
		if code, _, _ := rotaIn(t, dir, "milestone", "status", "M01", "--to", st); code != 0 {
			t.Errorf("--to %s ran the audit: exit %d", st, code)
		}
	}
	code, out, _ = rotaIn(t, dir, "milestone", "status", "M01", "--to", "shipped", "--force", "--json")
	if d := data(t, out); code != 0 || d["status"] != "shipped" || d["changed"] != true {
		t.Errorf("--force: %d %s", code, out)
	}
	done := "---\nid: M02\ntitle: Second\nstatus: planned\n---\n# M02\n\n## Acceptance\n\n- [x] AC-1: one\n"
	if code, _, _ := rotaStdin(t, dir, done, "milestone", "put", "M02", "--body-file", "-"); code != 0 {
		t.Fatalf("put M02: %d", code)
	}
	if code, out, _ := rotaIn(t, dir, "milestone", "status", "M02", "--to", "shipped", "--json"); code != 0 || data(t, out)["changed"] != true {
		t.Errorf("clean milestone: %d %s", code, out)
	}
}
