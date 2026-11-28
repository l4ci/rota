package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/proc"
	"github.com/l4ci/rota/internal/tracker"
)

// In an umbrella the branch lives in a sub-repo, so its open PR and its issue's
// labels are read there (#4422). The umbrella root is a repo with a forge of its
// own that holds a same-named branch's PR: any read of it fails the test.
func TestUmbrellaReviewReadsTheSubRepoForge(t *testing.T) {
	root := newRepo(t, t.TempDir(), "umb", "main")
	root, _ = filepath.EvalSymlinks(root)
	write(t, filepath.Join(root, ".rota", "config.json"),
		`{"forge":{"provider":"github"},"issues":{"source":"github","homeRepo":"web"},`+
			`"ship":{"review":{"default":"light","labels":{"risky":"full"}}}}`)
	write(t, filepath.Join(root, ".rota", "repos.json"), `{"repos":[{"name":"web","path":"web"}]}`)

	web := newRepo(t, root, "web", "main")
	gittest.Run(t, web, "branch", "milestone")
	gittest.Run(t, web, "checkout", "-q", "-b", "nia/6-docs")
	write(t, filepath.Join(web, "docs", "a.md"), "a\n")
	gittest.Run(t, web, "add", "docs/a.md")
	gittest.Run(t, web, "commit", "-q", "-m", "docs")

	deps := testDeps()
	deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(_ context.Context, cmd proc.Cmd) (proc.Result, error) {
		if cmd.Dir != web {
			t.Errorf("forge asked in %q, want the sub-repo: %v", cmd.Dir, cmd.Args)
		}
		switch {
		case len(cmd.Args) > 1 && cmd.Args[0] == "pr" && cmd.Args[1] == "list":
			return proc.Result{Stdout: `[{"number":9,"title":"t","isCrossRepository":false,"headRefName":"nia/6-docs","baseRefName":"milestone","url":"u","body":"Door: one-way. Blast radius: x."}]`}, nil
		case len(cmd.Args) > 2 && cmd.Args[0] == "issue" && cmd.Args[1] == "view":
			return proc.Result{Stdout: `{"number":6,"title":"t","state":"OPEN","body":"","labels":[{"name":"risky"}]}`}, nil
		}
		return proc.Result{Stdout: "[]"}, nil
	}, func(n string) (string, error) { return "/fake/" + n, nil })}

	data := func(args ...string) map[string]any {
		t.Helper()
		o := trRunWith(t, deps, root, "", append(args, "nia/6-docs", "--repo", "web", "--json")...)
		if o.code != 0 {
			t.Fatalf("%v: exit %d: %s%s", args, o.code, o.stdout, o.stderr)
		}
		d, _ := envelope(t, o.stdout)["data"].(map[string]any)
		return d
	}

	// review span: the PR names the base
	pkg, err := os.ReadFile(data("review", "package")["path"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if head, _, _ := strings.Cut(string(pkg), "\n"); head != "# Review package: nia/6-docs vs milestone" {
		t.Errorf("review package: %q", head)
	}
	// change tier: the PR body's Door line
	if d := data("change", "classify"); !strings.Contains(d["reason"].(string), "Door: one-way") {
		t.Errorf("change classify: %v", d)
	}
	// review depth: the issue's labels
	if d := data("review", "depth"); d["baseSource"] != "pr" || d["depth"] != "full" || d["signal"] != "label" {
		t.Errorf("review depth: %v", d)
	}
}
