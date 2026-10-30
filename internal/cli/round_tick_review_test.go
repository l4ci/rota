package cli

import (
	"context"
	"fmt"
	"github.com/l4ci/rota/internal/gittest"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/tracker"
	"github.com/l4ci/rota/internal/verdict"
	"github.com/l4ci/rota/internal/worker"
)

func reviewVerdictsMissing(c *Ctx, root, slot, base string) string {
	why, _ := reviewReadiness(c, root, slot, base)
	return why
}

func TestAutopilotReviewProbeLetsPRStateReachGate(t *testing.T) {
	for _, state := range []string{"CLOSED", "MERGED", "OPEN"} {
		t.Run(state, func(t *testing.T) {
			const branch = "ben/2185-x"
			dir := nudgeProject(t, []string{branch}, nil)
			remote := t.TempDir()
			gittest.Run(t, remote, "init", "--bare", "-q")
			gittest.Run(t, dir, "remote", "add", "origin", remote)
			gittest.Run(t, dir, "push", "-q", "origin", "main", branch)
			write(t, filepath.Join(dir, ".rota", "config.json"), `{"forge":{"provider":"github"},"ship":{"review":"full"}}`)
			addRegistryRows(t, dir, map[string][]map[string]any{
				"slots": {{"name": "ben", "state": "done", "branch": branch, "pr": "#11", "task": "2185"}},
			})
			deps := testDeps()
			// An open PR targeting another base is also an identity refusal,
			// not evidence that the head is waiting for review.
			deps.TrackerOptions = []tracker.Option{tracker.WithExec(func(context.Context, string, string, []string, []byte) ([]byte, []byte, int, error) {
				return []byte(fmt.Sprintf(`{"state":%q,"headRefName":%q,"baseRefName":"other"}`, state, branch)), nil, 0, nil
			}, func(n string) (string, error) { return "/fake/" + n, nil })}
			c := &Ctx{Deps: deps, ctx: context.Background(), Stderr: io.Discard}
			probe, err := workerEnvCtx(c, c.Context()).Gate(c.Context(), dir, worker.GateOpts{Slot: "ben", Base: "main", ReviewsOnly: true})
			if err != nil || probe.Verdict != worker.GatePRMismatch {
				t.Fatalf("fixture did not reach PR state refusal: %+v %v", probe, err)
			}
			if why := reviewVerdictsMissing(c, dir, "ben", "main"); why != "" {
				t.Fatalf("PR state refusal must reach the real gate, got review wait: %s", why)
			}
		})
	}
}

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
