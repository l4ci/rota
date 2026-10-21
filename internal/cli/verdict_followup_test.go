package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fileDeferredRepo is a file-backend repo with an empty backlog.
func fileDeferredRepo(t *testing.T) string {
	t.Helper()
	dir := verdictRepo(t)
	for f, c := range map[string]string{"BACKLOG.md": "# TODO\n\n## Tasks\n\n## Completed\n", "counters.json": "{}\n"} {
		if err := os.WriteFile(filepath.Join(dir, ".rota", f), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func fileDeferred(t *testing.T, dir, kind, findings string) (int, map[string]any, string) {
	t.Helper()
	body := writeBody(t, t.TempDir(), `{"findings":[`+findings+`]}`)
	code, out, errOut := rotaIn(t, dir, "verdict", "add", "--kind", kind, "--verdict", "FAIL", "--body-file", body, "--file-deferred", "--pr", "#77", "--json")
	if code != 0 {
		return code, nil, errOut
	}
	return code, data(t, out), errOut
}

func readTask(t *testing.T, dir, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".rota", "tasks", id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// #1599 AC-1/AC-4: several deferred findings make one item for the PR, one
// acceptance line each, the findings under Problem.
func TestFileDeferredOneItemPerPR(t *testing.T) {
	dir := fileDeferredRepo(t)
	code, d, errOut := fileDeferred(t, dir, "review-quality",
		`{"severity":"minor","title":"rename helper","file":"a.go","line":7,"detail":"foo is vague","fix":"helper is named bar"},{"severity":"major","title":"missing row","file":"b_test.go","blocking":false}`)
	if code != 0 {
		t.Fatalf("add: %d %s", code, errOut)
	}
	if filed, _ := d["filed"].([]any); len(filed) != 1 {
		t.Fatalf("filed = %v, want one item", d["filed"])
	}
	if d["appended"] != 2.0 {
		t.Errorf("appended = %v, want 2", d["appended"])
	}
	text := readTask(t, dir, "T01")
	for _, want := range []string{"## Problem", "rename helper", "a.go:7", "foo is vague", "missing row", "b_test.go", "#77",
		"- [ ] AC-1: helper is named bar", "- [ ] AC-2: the problem above no longer reproduces"} {
		if !strings.Contains(text, want) {
			t.Errorf("T01.md lacks %q:\n%s", want, text)
		}
	}
	bl, _ := os.ReadFile(filepath.Join(dir, ".rota", "BACKLOG.md"))
	if !strings.Contains(string(bl), "Review follow-ups from PR #77") {
		t.Errorf("title does not name the PR:\n%s", bl)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "tasks", "T02.md")); err == nil {
		t.Error("a second item was filed")
	}
}

// #1599 AC-2: a later call for the same PR appends; a repeated finding is not added twice.
func TestFileDeferredAppendsToOpenItem(t *testing.T) {
	dir := fileDeferredRepo(t)
	if code, _, errOut := fileDeferred(t, dir, "review-spec", `{"severity":"minor","title":"first","file":"a.go"}`); code != 0 {
		t.Fatalf("first: %d %s", code, errOut)
	}
	code, d, errOut := fileDeferred(t, dir, "review-quality",
		`{"severity":"minor","title":"first","file":"a.go"},{"severity":"minor","title":"second","file":"c.go"}`)
	if code != 0 {
		t.Fatalf("second: %d %s", code, errOut)
	}
	filed, _ := d["filed"].([]any)
	if len(filed) != 1 || d["appended"] != 1.0 {
		t.Fatalf("filed=%v appended=%v, want the existing item and 1 new line", d["filed"], d["appended"])
	}
	text := readTask(t, dir, "T01")
	if strings.Count(text, "- [ ] AC-") != 2 || !strings.Contains(text, "AC-2:") || strings.Count(text, "### first") != 1 || !strings.Contains(text, "### second") {
		t.Errorf("T01.md after append:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(dir, ".rota", "tasks", "T02.md")); err == nil {
		t.Error("a second item was filed")
	}
	// Same call again: nothing new.
	code, d, _ = fileDeferred(t, dir, "review-quality", `{"severity":"minor","title":"second","file":"c.go"}`)
	if code != 0 || d["appended"] != 0.0 {
		t.Errorf("retry: %d appended=%v", code, d["appended"])
	}
}

// #1599 AC-3: the floor decides what is filed.
func TestFileDeferredFloor(t *testing.T) {
	for name, tc := range map[string]struct {
		floor     string
		wantLines float64
	}{"minor": {"minor", 2}, "major": {"major", 1}, "off": {"off", 0}} {
		t.Run(name, func(t *testing.T) {
			dir := fileDeferredRepo(t)
			if err := os.WriteFile(filepath.Join(dir, ".rota", "config.json"), []byte(`{"review":{"fileDeferred":"`+tc.floor+`"}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			code, d, errOut := fileDeferred(t, dir, "review-quality", `{"severity":"minor","title":"nit"},{"severity":"major","title":"big","blocking":false}`)
			if code != 0 {
				t.Fatalf("add: %d %s", code, errOut)
			}
			if d["appended"] != tc.wantLines {
				t.Errorf("appended = %v, want %v", d["appended"], tc.wantLines)
			}
			if tc.floor == "off" {
				if filed, _ := d["filed"].([]any); len(filed) != 0 {
					t.Errorf("filed = %v under off", filed)
				}
				if why, _ := d["filedWhy"].(string); !strings.Contains(why, "review.fileDeferred") {
					t.Errorf("filedWhy = %q, want it to name the key", why)
				}
			}
		})
	}
}

func TestConfigRefusesUnknownFileDeferred(t *testing.T) {
	dir := fileDeferredRepo(t)
	if code, _, _ := rotaIn(t, dir, "config", "set", "review.fileDeferred", "sometimes"); code == 0 {
		t.Error("unknown value accepted")
	}
	if code, _, errOut := rotaIn(t, dir, "config", "set", "review.fileDeferred", "major"); code != 0 {
		t.Errorf("major refused: %s", errOut)
	}
}
