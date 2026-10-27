package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/git"
	"github.com/l4ci/rota/internal/pidlive"
	"github.com/l4ci/rota/internal/rotastate"
)

type landingAnswer struct {
	verdict string
	changed bool
	message string
	err     error
}

func landingGate(e Env, root, target string, noWait, checkOnly bool) landingAnswer {
	r, err := e.Gate(bg, root, GateOpts{Slot: target, Base: "main", NoWait: noWait, CheckOnly: checkOnly})
	return landingAnswer{r.Verdict, r.Changed, r.Err, err}
}

func landingTrain(e Env, root, target string, noWait, plan bool) landingAnswer {
	r, err := e.Train(bg, root, TrainOpts{Targets: []string{target}, Base: "main", NoWait: noWait, Plan: plan})
	return landingAnswer{r.Verdict, r.Changed, r.Err, err}
}

func landingAwait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("landing operation did not return promptly")
		var zero T
		return zero
	}
}

func landingCall(t *testing.T, fn func() landingAnswer) landingAnswer {
	t.Helper()
	done := make(chan landingAnswer, 1)
	go func() { done <- fn() }()
	return landingAwait(t, done)
}

// Keep a real repository land lock held until release. Cleanup releases it even
// when a refusal unexpectedly blocks, so a failing test cannot wedge the suite.
func landingHoldLock(t *testing.T, e Env, root string) func() {
	t.Helper()
	held := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- e.withLandLock(bg, root, "test holder", func(bool) error {
			close(held)
			<-release
			return nil
		})
	}()
	var once sync.Once
	unlock := func() {
		once.Do(func() {
			close(release)
			if err := landingAwait(t, done); err != nil {
				t.Errorf("land lock: %v", err)
			}
		})
	}
	t.Cleanup(unlock)
	landingAwait(t, held)
	return unlock
}

func landingMarkers(t *testing.T, common string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(rotastate.Dir(common), "landing-run-*.pid"))
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestLandingQueuedManualRunDefersNoWaitGateAndTrain(t *testing.T) {
	for _, manual := range []string{"gate", "train"} {
		t.Run(manual, func(t *testing.T) {
			w := trainWorld(t, "true", "b1", "b2", "b3")
			e := w.env(false).WithDefaults()
			common, err := rotastate.CommonDir(w.dir)
			if err != nil {
				t.Fatal(err)
			}
			unlock := landingHoldLock(t, e, w.dir)

			// Pause the manual caller at the external git lookup immediately
			// before lock entry, once its durable intent is visible. This makes
			// the queued state deterministic without sleeping or racing a poll.
			queued := make(chan struct{})
			proceed := make(chan struct{})
			var resumeOnce, queuedOnce sync.Once
			resume := func() { resumeOnce.Do(func() { close(proceed) }) }
			t.Cleanup(resume)
			manualEnv := e
			manualEnv.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
				if slices.Equal(args, []string{"rev-parse", "--git-common-dir"}) {
					paths, _ := filepath.Glob(filepath.Join(rotastate.Dir(common), "landing-run-*.pid"))
					if len(paths) != 0 {
						queuedOnce.Do(func() {
							close(queued)
							<-proceed
						})
					}
				}
				return e.Git(ctx, dir, args...)
			}
			done := make(chan landingAnswer, 1)
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				if manual == "gate" {
					done <- landingGate(manualEnv, w.dir, "b1", false, false)
				} else {
					done <- landingTrain(manualEnv, w.dir, "b1", false, false)
				}
			}()
			t.Cleanup(func() {
				resume()
				unlock()
				landingAwait(t, finished)
			})
			landingAwait(t, queued)

			// Admission may resolve the common directory, but must not enter
			// freshness, fetch, scratch-worktree or merge work on either path.
			var work atomic.Int32
			probe := e
			probe.Git = func(ctx context.Context, dir string, args ...string) (git.Result, error) {
				if !slices.Equal(args, []string{"rev-parse", "--git-common-dir"}) {
					work.Add(1)
				}
				return e.Git(ctx, dir, args...)
			}
			assertBusy := func() {
				t.Helper()
				for _, run := range []struct {
					name string
					fn   func() landingAnswer
				}{
					{"gate", func() landingAnswer { return landingGate(probe, w.dir, "b2", true, false) }},
					{"train", func() landingAnswer { return landingTrain(probe, w.dir, "b3", true, false) }},
				} {
					got := landingCall(t, run.fn)
					if got.err != nil || got.verdict != GateRunning || got.changed {
						t.Errorf("NoWait %s: %+v", run.name, got)
					}
				}
				if n := work.Load(); n != 0 {
					t.Errorf("deferred callers entered git work %d times", n)
				}
				if log := w.logText(); log != "" {
					t.Errorf("deferred callers contacted the forge: %s", log)
				}
			}
			assertBusy()
			unlock()
			// The lock is now free, but the queued manual intent still wins.
			assertBusy()
			resume()
			got := landingAwait(t, done)
			if got.err != nil || got.verdict != GatePass || !got.changed || !w.onMain("b1.txt") {
				t.Fatalf("manual %s after release: %+v", manual, got)
			}
			for _, retry := range []func() landingAnswer{
				func() landingAnswer { return landingGate(e, w.dir, "b2", true, false) },
				func() landingAnswer { return landingTrain(e, w.dir, "b3", true, false) },
			} {
				if got := landingCall(t, retry); got.err != nil || got.verdict != GatePass || !got.changed {
					t.Fatalf("retry after manual completion: %+v", got)
				}
			}
			if !w.onMain("b2.txt") || !w.onMain("b3.txt") {
				t.Error("successful retries did not land both branches")
			}
			if paths := landingMarkers(t, common); len(paths) != 0 {
				t.Errorf("finished runs left intent markers: %v", paths)
			}
		})
	}
}

func TestLandingNoWaitRefusesUnmarkedLandLock(t *testing.T) {
	w := trainWorld(t, "true", "b1")
	e := w.env(false).WithDefaults()
	common, err := rotastate.CommonDir(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	unlock := landingHoldLock(t, e, w.dir)
	if paths := landingMarkers(t, common); len(paths) != 0 {
		t.Fatalf("fixture unexpectedly has intent markers: %v", paths)
	}
	for _, fn := range []func() landingAnswer{
		func() landingAnswer { return landingGate(e, w.dir, "b1", true, false) },
		func() landingAnswer { return landingTrain(e, w.dir, "b1", true, false) },
	} {
		if got := landingCall(t, fn); got.err != nil || got.verdict != GateRunning || got.changed {
			t.Errorf("unmarked holder: %+v", got)
		}
	}
	unlock()
	if got := landingCall(t, func() landingAnswer { return landingTrain(e, w.dir, "b1", true, false) }); got.err != nil || got.verdict != GatePass || !got.changed {
		t.Fatalf("retry after unmarked holder: %+v", got)
	}
}

func TestLandingAbandonedIntentDoesNotWedgeRuns(t *testing.T) {
	for _, stale := range []string{"dead", "reused"} {
		for _, verb := range []string{"gate", "train"} {
			t.Run(stale+"/"+verb, func(t *testing.T) {
				body := "2147483646 1\n"
				if stale == "reused" {
					start, ok := pidlive.StartTicks(os.Getpid())
					if !ok {
						t.Skip("no process start time on this platform")
					}
					body = fmt.Sprintf("%d %d\n", os.Getpid(), start+1)
				}
				w := trainWorld(t, "true", "b1")
				e := w.env(false).WithDefaults()
				common, err := rotastate.CommonDir(w.dir)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(rotastate.Dir(common), 0o755); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(rotastate.Dir(common), "landing-run-abandoned.pid")
				if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
				got := landingCall(t, func() landingAnswer {
					if verb == "gate" {
						return landingGate(e, w.dir, "b1", true, false)
					}
					return landingTrain(e, w.dir, "b1", true, false)
				})
				if got.err != nil || got.verdict != GatePass || !got.changed || !w.onMain("b1.txt") {
					t.Fatalf("abandoned %s marker blocked %s: %+v", stale, verb, got)
				}
				if paths := landingMarkers(t, common); len(paths) != 0 {
					t.Errorf("abandoned or completed intent remains: %v", paths)
				}
			})
		}
	}
}

func TestLandingReadOnlyGateAndTrainBypassIntentAndLock(t *testing.T) {
	w := trainWorld(t, "false", "b1") // a preview must not verify or land
	e := w.env(false).WithDefaults()
	common, err := rotastate.CommonDir(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	landingHoldLock(t, e, w.dir)
	marker := filepath.Join(rotastate.Dir(common), "landing-run-live.pid")
	if err := os.WriteFile(marker, []byte(gateMarker(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, preview := range []struct {
		want string
		fn   func() landingAnswer
	}{
		{GateFresh, func() landingAnswer { return landingGate(e, w.dir, "b1", true, true) }},
		{GatePlan, func() landingAnswer { return landingTrain(e, w.dir, "b1", true, true) }},
	} {
		if got := landingCall(t, preview.fn); got.err != nil || got.verdict != preview.want || got.changed {
			t.Errorf("read-only run: %+v; want %s", got, preview.want)
		}
	}
	if w.onMain("b1.txt") {
		t.Error("read-only run landed the branch")
	}
	if paths := landingMarkers(t, common); !slices.Equal(paths, []string{marker}) {
		t.Errorf("read-only runs changed landing intent: %v", paths)
	}
}
