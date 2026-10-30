package cli

import (
	"context"
	"github.com/l4ci/rota/internal/gittest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/tracker"
)

// AC-1..3, AC-7: rota change classify prints the tier of a branch's diff
// against the base, with the deciding files, and fails closed to full.
func TestChangeClassify(t *testing.T) {
	root := newRepo(t, t.TempDir(), "proj", "main")
	gittest.Run(t, root, "checkout", "-q", "-b", "nia/6-docs")
	write(t, filepath.Join(root, "README.md"), "moved image\n")
	write(t, filepath.Join(root, "docs", "a.md"), "a\n")
	gittest.Run(t, root, "add", "README.md", "docs/a.md")
	gittest.Run(t, root, "commit", "-q", "-m", "docs")
	prs, prCode := `[]`, 0
	deps := testDeps()
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, _, _ string, args []string, _ []byte) ([]byte, []byte, int, error) {
		if len(args) > 1 && args[0] == "pr" && args[1] == "list" {
			if prCode != 0 {
				return nil, []byte("HTTP 500: server error"), prCode, nil
			}
			return []byte(prs), nil, 0, nil
		}
		return []byte("[]"), nil, 0, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}
	cfg := func(change string) {
		write(t, filepath.Join(root, ".rota", "config.json"), `{"forge":{"provider":"github"},"issues":{"source":"github"}`+change+`}`)
	}
	classify := func(args ...string) (int, map[string]any, string) {
		o := trRunWith(t, deps, root, "", append([]string{"change", "classify"}, args...)...)
		var d map[string]any
		if slices.Contains(args, "--json") {
			d, _ = envelope(t, o.stdout)["data"].(map[string]any)
		}
		return o.code, d, o.stdout + o.stderr
	}
	files := func(d map[string]any) string {
		var out []string
		for _, f := range d["files"].([]any) {
			out = append(out, f.(string))
		}
		return strings.Join(out, ",")
	}
	cfg("")

	code, d, out := classify("--json")
	if code != 0 || d["tier"] != "docs" || d["branch"] != "nia/6-docs" || d["base"] != "main" || files(d) != "README.md,docs/a.md" {
		t.Fatalf("docs-only branch: %d %v\n%s", code, d, out)
	}
	if _, ok := d["changedLines"].(float64); !ok || !strings.Contains(d["reason"].(string), "README.md") {
		t.Errorf("docs-only data lacks changedLines or a reason naming the files: %v", d)
	}
	if code, _, out := classify(); code != 0 || !strings.Contains(out, "TIER nia/6-docs — docs (docs-only change: README.md, docs/a.md)") {
		t.Errorf("text form: %d\n%s", code, out)
	}

	// a one-way Door on the branch's open PR forces full
	prs = `[{"number":9,"title":"t","headRefName":"nia/6-docs","url":"u","body":"Door: one-way. Blast radius: x."}]`
	if _, d, _ := classify("--json"); d["tier"] != "full" || !strings.Contains(d["reason"].(string), "Door: one-way") {
		t.Errorf("one-way door: %v", d)
	}
	// an unreadable PR body fails closed
	prCode = 1
	if code, d, out := classify("--json"); code != 0 || d["tier"] != "full" || !strings.Contains(d["reason"].(string), "PR body") {
		t.Errorf("unreadable PR body must classify full: %d %v\n%s", code, d, out)
	}
	prs, prCode = `[]`, 0

	// a risky path forces full
	cfg(`,"change":{"riskyPaths":["docs/**"]}`)
	if _, d, _ := classify("--json"); d["tier"] != "full" || files(d) != "docs/a.md" || !strings.Contains(d["reason"].(string), "riskyPaths") {
		t.Errorf("risky path: %v", d)
	}
	// a malformed change policy fails closed, naming the key
	cfg(`,"change":{"smallBelow":-3}`)
	if code, d, out := classify("--json"); code != 0 || d["tier"] != "full" || !strings.Contains(d["reason"].(string), "change.smallBelow") {
		t.Errorf("bad config must classify full: %d %v\n%s", code, d, out)
	}
	cfg("")

	// any unrecognised file makes it full
	write(t, filepath.Join(root, "main.go"), "package main\n")
	gittest.Run(t, root, "add", "main.go")
	gittest.Run(t, root, "commit", "-q", "-m", "code")
	if _, d, _ := classify("--json"); d["tier"] != "full" || files(d) != "main.go" {
		t.Errorf("code file: %v", d)
	}
	// small when under change.smallBelow
	cfg(`,"change":{"smallBelow":100,"smallPaths":["**"]}`)
	if _, d, _ := classify("--json"); d["tier"] != "small" {
		t.Errorf("small diff: %v", d)
	}
	cfg("")
	// --base moves the span: against the docs commit only main.go changed
	gittest.Run(t, root, "branch", "docs-done", "HEAD~1")
	if _, d, _ := classify("--json", "--base", "docs-done"); d["tier"] != "full" || d["base"] != "docs-done" || files(d) != "main.go" {
		t.Errorf("--base: %v", d)
	}
	// a base that does not resolve fails closed
	if code, d, out := classify("--json", "--base", "nope"); code != 0 || d["tier"] != "full" || !strings.Contains(d["reason"].(string), "could not") {
		t.Errorf("unknown base must classify full: %d %v\n%s", code, d, out)
	}
}
