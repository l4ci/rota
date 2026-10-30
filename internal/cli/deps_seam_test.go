package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
)

// A swapped Deps.Git, Getenv and LookPath reach the round environment's base
// read and its pane-host decision.
func TestDefaultRoundEnvReadsThroughDeps(t *testing.T) {
	root := t.TempDir()
	d := defaultDeps()
	var gitCalls int
	d.Git = func(_ context.Context, _ string, args ...string) (git.Result, error) {
		gitCalls++
		if strings.Join(args, " ") == "rev-parse --verify -q trunk" {
			return git.Result{ExitCode: 0}, nil
		}
		return git.Result{ExitCode: 1}, nil
	}
	var envReads, lookups int
	d.Getenv = func(k string) string {
		envReads++
		if k == "HERDR_ENV" {
			return "1"
		}
		return ""
	}
	d.LookPath = func(string) (string, error) { lookups++; return "/fake/herdr", nil }

	e := defaultRoundEnv(context.Background(), root, d, false)

	if e.Base != "trunk" || gitCalls == 0 {
		t.Errorf("base read missed Deps.Git: base=%q calls=%d", e.Base, gitCalls)
	}
	if envReads == 0 || lookups == 0 {
		t.Errorf("pane host missed Deps seams: getenv=%d lookPath=%d", envReads, lookups)
	}
}

func TestDepsRepoRunsThroughGit(t *testing.T) {
	d := defaultDeps()
	d.Git = func(context.Context, string, ...string) (git.Result, error) {
		return git.Result{}, errors.New("fake git")
	}
	if _, err := d.Repo(t.TempDir()).Run(context.Background(), "status"); err == nil || err.Error() != "fake git" {
		t.Fatalf("err = %v, want fake git", err)
	}
}

// The round environment's labels are the ones the project's config names, not a
// default restated in the env build: each role is set to a value no default
// equals, so a hardcoded label fails.
func TestDefaultRoundEnvLabelsComeFromConfig(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".rota", "config.json"), `{"round":{"support":"mac"},"issues": {"labels": {"inProgress": "wip-x", "needsHuman": "human-x", "needsReview": "review-x", "support": "legacy-x"}}}`)
	e := defaultRoundEnv(context.Background(), root, defaultDeps(), false)
	for role, c := range map[string]struct{ got, want string }{
		"inProgress":  {e.Label, "wip-x"},
		"needsHuman":  {e.NeedsHuman, "human-x"},
		"needsReview": {e.NeedsReview, "review-x"},
		"support":     {e.SupportLabel, "machine:mac"},
	} {
		if c.got != c.want {
			t.Errorf("issues.labels.%s = %q, want the configured %q", role, c.got, c.want)
		}
	}
}

func TestDefaultRoundEnvDefaultLabels(t *testing.T) {
	e := defaultRoundEnv(context.Background(), t.TempDir(), defaultDeps(), false)
	if e.Label != "in-progress" || e.NeedsHuman != "needs-human" || e.NeedsReview != "needs-review" || e.SupportLabel != "" {
		t.Errorf("default labels = %q, %q, %q, %q", e.Label, e.NeedsHuman, e.NeedsReview, e.SupportLabel)
	}
}

// Ctx.Root walks up from Deps.Getwd, not the process cwd, so a test resolves a
// root without a Chdir.
func TestCtxRootReadsDepsGetwd(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".rota"), 0o755); err != nil {
		t.Fatal(err)
	}
	d := defaultDeps()
	d.Getwd = func() (string, error) { return deep, nil }
	c := &Ctx{Deps: d}
	got, err := c.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	want, _ := filepath.EvalSymlinks(root)
	if g, _ := filepath.EvalSymlinks(got); g != want {
		t.Errorf("Root = %q, want %q", got, root)
	}
}

// The round environment's base is git.baseBranch when it resolves, even in a
// repo that also has main.
func TestDefaultRoundEnvBaseHonoursConfiguredBranch(t *testing.T) {
	root := t.TempDir()
	gittest.Init(t, root, "main")
	gittest.Run(t, root, "commit", "-q", "--allow-empty", "-m", "init")
	gittest.Run(t, root, "branch", "develop")
	write(t, filepath.Join(root, ".rota", "config.json"), `{"git": {"baseBranch": "develop"}}`)
	e := defaultRoundEnv(context.Background(), root, defaultDeps(), false)
	if e.Base != "develop" {
		t.Errorf("Base = %q, want the configured develop", e.Base)
	}
}
