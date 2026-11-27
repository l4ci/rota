package worker

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/gittest"
	"github.com/l4ci/rota/internal/landing"
	"github.com/l4ci/rota/internal/ledger"
)

// Observe the process boundary through Train: callers need not bind Env.Ctx
// to the operation for ref reads, footprint discovery, diagnosis or integration.
func TestTrainInvocationContext(t *testing.T) {
	for _, scenario := range []string{"preview", "diagnosis", "integration"} {
		t.Run(scenario, func(t *testing.T) {
			var w *world
			opts := TrainOpts{Base: "main", Targets: []string{"b1", "b2"}}
			want := landing.GateVerifyFailed
			switch scenario {
			case "preview":
				w = orderWorld(t)
				opts.Plan, want = true, landing.GatePlan
			case "diagnosis":
				w = trainWorld(t, "! test -f b2.txt", "b1", "b2")
			case "integration":
				w = integrationWorld(t)
				opts.Targets, opts.Integration = []string{"7", "8"}, "a"
				want = landing.GatePass
			}
			type key struct{}
			ctx, cancel := context.WithTimeout(context.WithValue(bg, key{}, "invocation"), time.Minute)
			defer cancel()
			deadline, _ := ctx.Deadline()
			e := w.env(false).WithDefaults()
			envCtx, envCancel := context.WithCancel(context.WithValue(bg, key{}, "environment"))
			envCancel()
			e.Ctx = envCtx
			real := e.Git
			seen := map[string]bool{}
			e.Git = func(c context.Context, dir string, args ...string) (git.Result, error) {
				// Worktree cleanup and ledger reads have their own lifetimes.
				// These normal train operations must retain the invocation deadline.
				if len(args) > 0 && (args[0] == "rev-parse" || args[0] == "diff" || args[0] == "merge-base" || args[0] == "merge-tree" || args[0] == "checkout" || args[0] == "merge" || args[0] == "push") && !strings.Contains(strings.Join(args, " "), "--git-common-dir") {
					seen[args[0]] = true
					if c.Value(key{}) != "invocation" {
						t.Errorf("git %v received context value %v, want invocation", args, c.Value(key{}))
					}
					if got, ok := c.Deadline(); !ok || !got.Equal(deadline) {
						t.Errorf("git %v lost invocation deadline: %v, %v", args, got, ok)
					}
				}
				return real(c, dir, args...)
			}
			for run := 0; run < 2; run++ {
				res, err := e.Train(ctx, w.dir, opts)
				if err != nil || res.Verdict != want {
					t.Fatalf("train: %+v, %v", res, err)
				}
				if scenario != "integration" {
					break
				}
			}
			for _, command := range map[string][]string{
				"preview":     {"rev-parse", "diff", "merge-tree"},
				"diagnosis":   {"rev-parse", "checkout", "merge"},
				"integration": {"rev-parse", "merge-base", "merge", "push"},
			}[scenario] {
				if !seen[command] {
					t.Errorf("scenario never exercised git %s", command)
				}
			}
		})
	}
}

func TestTrainCancelAtRefRead(t *testing.T) {
	w := trainWorld(t, "true", "b1")
	e := w.env(false).WithDefaults()
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	real := e.Git
	stopped := false
	e.Git = func(c context.Context, dir string, args ...string) (git.Result, error) {
		if strings.Join(args, " ") == "rev-parse main" {
			cancel()
			stopped = c.Err() == context.Canceled
		}
		return real(c, dir, args...)
	}
	res, err := e.Train(ctx, w.dir, TrainOpts{Base: "main", Targets: []string{"b1"}})
	if !stopped || err != nil || res.Verdict != landing.GateCancelled || len(res.Landed) != 0 || res.Culprit != "" {
		t.Fatalf("planning ref read must observe cancellation: stopped=%v result=%+v err=%v", stopped, res, err)
	}
	if w.onMain("b1.txt") {
		t.Fatal("cancelled planning landed a member")
	}
}

func TestTrainCancelDuringMemberLanding(t *testing.T) {
	w := trainWorld(t, "true", "b1", "b2")
	e := w.env(false).WithDefaults()
	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(bg, key{}, "invocation"))
	defer cancel()
	e.Ctx = context.WithValue(bg, key{}, "environment")
	real := e.Git
	started := false
	e.Git = func(c context.Context, dir string, args ...string) (git.Result, error) {
		if c.Value(key{}) != "invocation" {
			t.Errorf("git %v received context value %v, want invocation", args, c.Value(key{}))
		}
		if dir == w.dir && len(args) > 0 && args[0] == "merge" {
			started = true
			cancel()
			if c.Err() != nil {
				t.Errorf("member landing lost its cancellation shield: %v", c.Err())
			}
		}
		return real(c, dir, args...)
	}
	res, err := e.Train(ctx, w.dir, TrainOpts{Base: "main", Targets: []string{"b1", "b2"}})
	if !started || err != nil || res.Verdict != landing.GateCancelled || strings.Join(res.Landed, ",") != "b1" || !res.Changed || res.Culprit != "" {
		t.Fatalf("finish the started member, stop before the next: started=%v result=%+v err=%v", started, res, err)
	}
	if !w.onMain("b1.txt") || w.onMain("b2.txt") {
		t.Fatal("landed files disagree with the reported prefix")
	}
	if out := gittest.Run(t, w.dir, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Errorf("scratch worktree left behind: %s", out)
	}
	if held, err := w.env(false).WithDefaults().LandLockHeld(bg, w.dir); held || err != nil {
		t.Errorf("land lock still held: %v %v", held, err)
	}
	entries, err := ledger.Load(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Kind == ledger.KindTrain && entry.Verdict() == landing.GateCancelled {
			return
		}
	}
	t.Fatalf("cancelled train bookkeeping missing: %+v", entries)
}
