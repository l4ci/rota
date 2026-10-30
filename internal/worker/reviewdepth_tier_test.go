package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/change"
	"github.com/l4ci/rota/internal/config"
	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/tracker"
)

// tierRepo is a repo whose branch feat changes only the given files.
func tierRepo(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=a@b", "-c", "user.name=a"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("commit", "-q", "--allow-empty", "-m", "base")
	run("checkout", "-q", "-b", "feat")
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		body := []byte("x\n")
		if strings.HasSuffix(f, ".bin") {
			body = []byte{0, 1, 2, 0} // numstat shows a binary file as dashes
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", f)
	}
	run("commit", "-q", "-m", "change")
	return dir
}

// AC-6, AC-7: the change tier sets the review depth. A docs-only diff is none
// and never lifted by scope drift; small caps at light; a label, a Door line
// or a risky path keeps the policy's depth.
func TestResolveReviewDepthTier(t *testing.T) {
	docs := change.Policy{DocsPaths: change.DefaultDocsPaths}
	full, _ := config.ParseReviewPolicy("full")
	light, _ := config.ParseReviewPolicy("light")
	labelled, _ := config.ParseReviewPolicy(nil)
	labelled.Labels = map[string]config.ReviewDepth{"risk:high": config.DepthFull, "review:light": config.DepthLight}
	withPaths, _ := config.ParseReviewPolicy(nil)
	withPaths.Paths = map[string]config.ReviewDepth{"docs/sec/**": config.DepthFull}
	drift := fakeIssueGet{is: tracker.Issue{Body: "## Files\n\n- other.go\n"}}
	cases := []struct {
		name   string
		files  []string
		policy config.ReviewPolicy
		change change.Policy
		pr     string
		labels []string
		depth  config.ReviewDepth
		signal string
		why    string
	}{
		{"docs under full and drift", []string{"README.md", "docs/a.md"}, full, docs, "", nil, config.DepthNone, config.SignalTier, "docs-only change (tier docs)"},
		{"docs with a one-way door", []string{"README.md"}, light, docs, "Door: one-way.", nil, config.DepthFull, config.SignalDoor, "Door: one-way"},
		{"docs with a risky path", []string{"docs/a.md"}, full, change.Policy{DocsPaths: change.DefaultDocsPaths, RiskyPaths: []string{"docs/**"}}, "", nil, config.DepthFull, config.SignalDefault, "default depth"},
		{"docs with a full label", []string{"README.md"}, labelled, docs, "", []string{"risk:high"}, config.DepthFull, config.SignalLabel, "risk:high"},
		{"docs with a light label", []string{"README.md"}, labelled, docs, "", []string{"review:light"}, config.DepthLight, config.SignalLabel, "review:light"},
		{"small with drift", []string{"x.go"}, full, change.Policy{DocsPaths: change.DefaultDocsPaths, SmallBelow: 100, SmallPaths: []string{"**"}}, "", nil, config.DepthLight, config.SignalTier, "small change (tier small)"},
		{"docs under light, default policy", []string{"docs/a.md"}, light, docs, "", nil, config.DepthNone, config.SignalTier, "tier docs"},
		{"docs with a paths rule", []string{"docs/sec/a.md"}, withPaths, docs, "", nil, config.DepthFull, config.SignalPath, "docs/sec/**"},
		{"small with a binary file", []string{"x.go", "lib.bin"}, full, change.Policy{DocsPaths: change.DefaultDocsPaths, SmallBelow: 100, SmallPaths: []string{"**"}}, "", nil, config.DepthFull, config.SignalDefault, "default depth"},
		{"risky path under light", []string{"internal/x.go"}, light, change.Policy{DocsPaths: change.DefaultDocsPaths, RiskyPaths: []string{"internal/**"}}, "", nil, config.DepthFull, config.SignalRisky, "change.riskyPaths"},
		{"risky path with no other tier config", []string{"internal/x.go"}, light, change.Policy{RiskyPaths: []string{"internal/**"}}, "", nil, config.DepthFull, config.SignalRisky, "change.riskyPaths"},
		{"tiers off", []string{"README.md"}, light, change.Policy{}, "", nil, config.DepthFull, config.SignalScopeDrift, "README.md"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := tierRepo(t, c.files...)
			src := ReviewSources{Policy: c.policy, Change: c.change, Git: git.Exec, Dir: dir, Span: "main...feat",
				Labels: c.labels, Issue: tracker.NumKey(5), Issues: drift}
			if c.pr != "" {
				src.PRBody = func() (string, error) { return c.pr, nil }
			}
			got, err := ResolveReviewDepth(context.Background(), src)
			if err != nil {
				t.Fatal(err)
			}
			if got.Depth != c.depth || got.Signal != c.signal || !strings.Contains(got.Why, c.why) {
				t.Fatalf("depth = %s, %s (%s), want %s, %s (%s)", got.Depth, got.Signal, got.Why, c.depth, c.signal, c.why)
			}
		})
	}
	// a label of none stays final, as for a Door line
	dir0 := tierRepo(t, "internal/x.go")
	none, _ := config.ParseReviewPolicy(nil)
	none.Labels = map[string]config.ReviewDepth{"review:none": config.DepthNone}
	got0, err := ResolveReviewDepth(context.Background(), ReviewSources{Policy: none, Change: change.Policy{RiskyPaths: []string{"internal/**"}}, Git: git.Exec, Dir: dir0, Span: "main...feat", Labels: []string{"review:none"}})
	if err != nil || got0.Depth != config.DepthNone {
		t.Fatalf("review:none with a risky path: %s %v", got0.Depth, err)
	}
	// a malformed change policy never makes a depth cheaper
	dir := tierRepo(t, "README.md")
	got, err := ResolveReviewDepth(context.Background(), ReviewSources{Policy: full, ChangeErr: os.ErrInvalid, Change: docs, Git: git.Exec, Dir: dir, Span: "main...feat"})
	if err != nil || got.Depth != config.DepthFull || got.Tier.Tier != change.TierFull {
		t.Fatalf("bad change policy: %+v %v", got, err)
	}
}
