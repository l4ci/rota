package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func rotaStdin(t *testing.T, dir, stdin string, args ...string) (int, string, string) {
	t.Helper()
	old, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(old)
	var so, se bytes.Buffer
	code := Main(args, strings.NewReader(stdin), &so, &se)
	return code, so.String(), se.String()
}

func TestDesignVerbs(t *testing.T) {
	dir := gitRepo(t)
	if code, out, _ := rotaIn(t, dir, "design", "add", "B07", "--title", "T", "--json"); code != 0 {
		t.Fatalf("add: %d %s", code, out)
	} else if d := data(t, out); d["id"] != "B07" || d["type"] != "B" || d["changed"] != true {
		t.Fatalf("add data %v", d)
	}
	code, out, _ := rotaIn(t, dir, "design", "add", "B07", "--title", "T", "--json")
	if code != 4 || !strings.Contains(out, `"blockedBy": "exists"`) || !strings.Contains(out, `"changed": false`) {
		t.Errorf("duplicate: %d %s", code, out)
	}
	for _, args := range [][]string{{"design", "add", "S01", "--title", "T"}, {"design", "add", "B07"}, {"design", "show", "x"}, {"design", "put", "b1", "--body-file", "-"}, {"design", "put", "B07"}} {
		if code, _, _ := rotaIn(t, dir, args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code, _, _ := rotaStdin(t, dir, "## Goal\nnew\n", "design", "put", "B07", "--body-file", "-"); code != 0 {
		t.Errorf("put via stdin: %d", code)
	}
	if code, out, _ := rotaIn(t, dir, "design", "show", "B07"); code != 0 || out != "## Goal\nnew\n" {
		t.Errorf("show: %d %q", code, out)
	}
	if code, _, _ := rotaStdin(t, dir, "", "design", "put", "B07", "--body-file", filepath.Join(dir, "missing")); code != 2 {
		t.Errorf("unreadable body file: %d", code)
	}
	_, out, _ = rotaStdin(t, dir, "## Goal\nnew\n", "design", "put", "B07", "--body-file", "-", "--json")
	if data(t, out)["changed"] != false {
		t.Errorf("identical put: %s", out)
	}
	if _, out, _ = rotaIn(t, dir, "design", "list", "--json"); !strings.Contains(out, `"designs"`) {
		t.Errorf("list: %s", out)
	}
	_, out, _ = rotaStdin(t, dir, "more\n", "design", "amend", "B07", "--section", "Goal", "--mode", "append", "--body-file", "-", "--json")
	if d := data(t, out); d["changed"] != true || d["section"] != "Goal" || d["mode"] != "append" {
		t.Errorf("amend: %s", out)
	}
	if code, _, _ := rotaIn(t, dir, "design", "amend", "B07", "--section", "Goal", "--mode", "x", "--body-file", "-"); code != 2 {
		t.Errorf("amend bad mode: %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "design", "rm", "B07"); code != 0 {
		t.Errorf("rm: %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "design", "rm", "B07"); code != 3 {
		t.Errorf("second rm: %d", code)
	}
}

func TestPlanVerbs(t *testing.T) {
	dir := gitRepo(t)
	_, out, _ := rotaIn(t, dir, "plan", "add", "--milestone", "M01", "--slice", "--title", "One", "--json")
	if d := data(t, out); d["key"] != "M01-S01" || d["unitKind"] != "slice" || d["changed"] != true {
		t.Fatalf("add slice: %s", out)
	}
	_, out, _ = rotaIn(t, dir, "plan", "add", "M01-B07", "--title", "Two", "--json")
	if d := data(t, out); d["key"] != "M01-B07" || d["unitKind"] != "item" {
		t.Fatalf("add item: %s", out)
	}
	if code, _, _ := rotaIn(t, dir, "plan", "add", "M01-B07", "--title", "Two"); code != 4 {
		t.Errorf("duplicate: %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "plan", "add", "M01-B07", "--slice", "--title", "x"); code != 2 {
		t.Errorf("key with --slice: %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "plan", "add", "--slice", "--title", "x"); code != 2 {
		t.Errorf("--slice without --milestone: %d", code)
	}
	_, out, _ = rotaIn(t, dir, "plan", "list", "--milestone", "M01", "--json")
	if !strings.Contains(out, `"repos": []`) || strings.Count(out, `"key"`) != 2 {
		t.Errorf("list: %s", out)
	}
	if code, _, _ := rotaIn(t, dir, "plan", "list", "--milestone", "bad"); code != 2 {
		t.Errorf("list bad milestone: %d", code)
	}
	if code, _, _ := rotaIn(t, dir, "plan", "show", "M01-S01"); code != 0 {
		t.Errorf("show: %d", code)
	}
	if code, out, _ := rotaIn(t, dir, "plan", "show", "M09-B01"); code != 3 || out != "" {
		t.Errorf("show missing: %d %q", code, out)
	}
	_, out, _ = rotaIn(t, dir, "plan", "validate-docs", "M01-B07", "--json")
	if d := data(t, out); d["valid"] != true {
		t.Errorf("validate-docs: %s", out)
	}
	if code, _, _ := rotaIn(t, dir, "plan", "rm", "M01-S01"); code != 0 {
		t.Errorf("rm: %d", code)
	}
}

func TestPlanRenameCheckRunsInCwd(t *testing.T) {
	dir := gitRepo(t) // no .rota needed, but gitRepo makes one; rename-check must not use it
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("oldname\n"), 0o644)
	gitAdd := exec.Command("git", "add", "a.txt")
	gitAdd.Dir = dir
	gitAdd.Run()
	code, out, _ := rotaIn(t, dir, "plan", "rename-check", "oldname", "--json")
	if code != 0 || !strings.Contains(out, `"files": ["a.txt"]`) {
		t.Errorf("%d %s", code, out)
	}
	if code, out, _ := rotaIn(t, dir, "plan", "rename-check", "oldname", "--", "nowhere"); code != 0 || out != "" {
		t.Errorf("scoped: %d %q", code, out)
	}
}

func TestIssueModeFileOnlyVerbs(t *testing.T) {
	dir := gitRepo(t)
	os.WriteFile(filepath.Join(dir, ".rota/config.json"), []byte(`{"backlog": {"backend": "issues"}}`), 0o644)
	if code, _, _ := rotaIn(t, dir, "plan", "add", "M01-B07"); code != 2 {
		t.Errorf("argument errors stay exit 2 in issue mode, got %d", code)
	}
	for _, args := range [][]string{{"design", "list"}, {"plan", "validate-docs", "M01-B07"}} {
		if code, _, _ := rotaIn(t, dir, args...); code != 1 {
			t.Errorf("%v: exit %d, want 1 (read-only, file-only)", args, code)
		}
	}
}
