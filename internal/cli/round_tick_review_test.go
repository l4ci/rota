package cli

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/verdict"
)

func TestAutopilotReviewProbeMustConfirmReadiness(t *testing.T) {
	dir := nudgeProject(t, []string{"ben/2175-x"}, nil)
	addRegistryRows(t, dir, map[string][]map[string]any{
		"slots": {{"name": "ben", "state": "done", "branch": "ben/2175-x", "pr": "#11", "issue": "2175"}},
	})
	c := &Ctx{Deps: testDeps(), ctx: context.Background(), Stderr: io.Discard}
	// No origin: the probe stops before it can read the head's reviews.
	// That cannot grant the autopilot permission to run a real gate.
	if why := reviewVerdictsMissing(c, dir, "ben", "main"); why == "" {
		t.Fatal("autopilot treated an incomplete review probe as ready to gate")
	}
}

func TestAutopilotWaitsForEveryReviewAtHead(t *testing.T) {
	dir := nudgeProject(t, []string{"ben/2175-x"}, nil)
	write(t, filepath.Join(dir, ".rota", "config.json"), `{"ship":{"review":"full"}}`)
	addRegistryRows(t, dir, map[string][]map[string]any{
		"slots": {{"name": "ben", "state": "done", "branch": "ben/2175-x", "task": "2175"}},
	})
	deps := testDeps()
	c := &Ctx{Deps: deps, ctx: context.Background(), Stderr: io.Discard}
	res, err := deps.Git(c.Context(), dir, "rev-parse", "ben/2175-x")
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("head: %+v %v", res, err)
	}
	head := strings.TrimSpace(res.Stdout)
	add := func(kind, sha string) {
		t.Helper()
		if _, err := verdict.AddBranch(dir, "ben/2175-x", verdict.NewRecord(kind, verdict.Pass, sha, verdict.Body{}, time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	check := func(wait bool) {
		t.Helper()
		why := reviewVerdictsMissing(c, dir, "ben", "main")
		if (why != "") != wait {
			t.Fatalf("waiting = %t, want %t: %s", why != "", wait, why)
		}
	}
	check(true)
	add(verdict.ReviewSpec, head)
	check(true)
	add(verdict.ReviewQuality, strings.Repeat("0", 40))
	check(true)
	add(verdict.ReviewQuality, head)
	check(false)
}
