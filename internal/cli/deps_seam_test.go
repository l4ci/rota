package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/l4ci/rota/internal/git"
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
	write(t, filepath.Join(root, ".rota", "config.json"), `{"issues": {"labels": {"inProgress": "wip-x", "needsHuman": "human-x", "needsReview": "review-x", "support": "support-x"}}}`)
	e := defaultRoundEnv(context.Background(), root, defaultDeps(), false)
	for role, c := range map[string]struct{ got, want string }{
		"inProgress":  {e.Label, "wip-x"},
		"needsHuman":  {e.NeedsHuman, "human-x"},
		"needsReview": {e.NeedsReview, "review-x"},
		"support":     {e.SupportLabel, "support-x"},
	} {
		if c.got != c.want {
			t.Errorf("issues.labels.%s = %q, want the configured %q", role, c.got, c.want)
		}
	}
}
