package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/tracker"
)

// A fork PR that reuses the branch name is listed first. Base, Door line and
// change tier all come from the project's own PR, and a depth preview lists the
// open PRs once.
func TestBranchPRSkipsForkPR(t *testing.T) {
	root := newRepo(t, t.TempDir(), "proj", "main")
	gittest.Run(t, root, "checkout", "-q", "-b", "m/x")
	gittest.Run(t, root, "checkout", "-q", "-b", "nia/6-docs")
	write(t, filepath.Join(root, "docs", "a.md"), "a\n")
	gittest.Run(t, root, "add", "docs/a.md")
	gittest.Run(t, root, "commit", "-q", "-m", "docs")
	lists := 0
	deps := testDeps()
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
		if len(cmd.Args) > 1 && cmd.Args[0] == "pr" && cmd.Args[1] == "list" {
			lists++
			return proc.Result{Stdout: `[{"number":8,"headRefName":"nia/6-docs","baseRefName":"main","body":"Door: two-way.","isCrossRepository":true},` +
				`{"number":9,"headRefName":"nia/6-docs","baseRefName":"m/x","body":"Door: one-way. Blast radius: x.","isCrossRepository":false}]`}, nil
		}
		return proc.Result{Stdout: "[]"}, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}
	write(t, filepath.Join(root, ".rota", "config.json"), `{"forge":{"provider":"github"},"ship":{"review":{"default":"full","lightBelow":100000}}}`)
	run := func(args ...string) map[string]any {
		o := trRunWith(t, deps, root, "", args...)
		d, _ := envelope(t, o.stdout)["data"].(map[string]any)
		return d
	}

	if d := run("review", "depth", "nia/6-docs", "--json"); d["base"] != "m/x" || d["depth"] != "full" || d["signal"] != "door" {
		t.Errorf("depth must read base and Door line from the own PR: %v", d)
	}
	if lists != 1 {
		t.Errorf("depth listed the open PRs %d times, want 1", lists)
	}
	if d := run("change", "classify", "nia/6-docs", "--json"); d["tier"] != "full" {
		t.Errorf("classify must read the own PR's one-way Door: %v", d)
	}
}
