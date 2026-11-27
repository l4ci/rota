package roundwatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/roundtick"
)

func autopilotRun(t *testing.T, trainFill bool, results ...roundtick.Result) (AutopilotResult, int) {
	t.Helper()
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, nil }, empty, empty)
	calls := 0
	res, err := RunAutopilot(context.Background(), env, Opts{Heartbeat: 3 * time.Minute, Poll: time.Minute}, AutopilotOpts{
		Tick: func() (roundtick.Result, error) {
			r := results[min(calls, len(results)-1)]
			calls++
			return r, nil
		},
		TrainFill: trainFill,
		Poll:      time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res, calls
}

// #2412 AC-1: the wake reason comes from the tick result inside roundwatch.
func TestAutopilotWakesOnANewItem(t *testing.T) {
	res, calls := autopilotRun(t, false, roundtick.Result{New: []roundtick.Item{{Kind: "slot", Target: "ben"}}})
	if res.Reason != ReasonChange || res.Tick == nil || len(res.Tick.New) != 1 || calls != 1 {
		t.Fatalf("res=%+v calls=%d", res, calls)
	}
}

func TestAutopilotWakesOnAMergeOnlyUnderTrainFill(t *testing.T) {
	merged := roundtick.Result{Did: []roundtick.Action{{Action: "merge", Target: "#9"}}}
	if res, _ := autopilotRun(t, true, merged); res.Reason != ReasonChange {
		t.Fatalf("train fill on: %+v", res)
	}
	if res, _ := autopilotRun(t, false, merged); res.Reason != ReasonHeartbeat {
		t.Fatalf("train fill off must stay quiet until the heartbeat: %+v", res)
	}
}

func TestAutopilotStopsAndFailsAsTypedResults(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, nil }, empty, empty)
	gone := roundtick.ErrNoLease
	run := func(err error) AutopilotResult {
		res, rerr := RunAutopilot(context.Background(), env, Opts{Heartbeat: time.Minute, Poll: time.Minute}, AutopilotOpts{
			Tick: func() (roundtick.Result, error) { return roundtick.Result{}, err },
			Poll: time.Minute,
		})
		if rerr != nil {
			t.Fatal(rerr)
		}
		return res
	}
	if res := run(gone); res.Reason != ReasonStopped || res.Tick != nil || !errors.Is(res.StopErr, roundtick.ErrNoLease) {
		t.Fatalf("stop: %+v", res)
	}
	boom := errors.New("boom")
	if res := run(boom); res.Reason != ReasonChange || res.TickErr != boom || res.Tick != nil {
		t.Fatalf("tick failure: %+v", res)
	}
}

// A stop seen mid-tick comes back as a nil error with Result.Stopped set: the
// watch stops at once and keeps that tick, so the merges it did are reported.
func TestAutopilotStopsOnAMidTickStop(t *testing.T) {
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, nil }, empty, empty)
	ticks := 0
	res, err := RunAutopilot(context.Background(), env, Opts{Heartbeat: time.Minute, Poll: time.Minute}, AutopilotOpts{
		Tick: func() (roundtick.Result, error) {
			ticks++
			return roundtick.Result{Stopped: roundtick.ErrStopped, Did: []roundtick.Action{{Action: roundtick.ActionMerge, Target: "#7"}}}, nil
		},
		Poll: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(res.StopErr, roundtick.ErrStopped) {
		t.Fatalf("StopErr = %v, want ErrStopped", res.StopErr)
	}
	if res.Reason != ReasonStopped || res.Tick == nil || len(res.Tick.Did) != 1 || ticks != 1 {
		t.Fatalf("mid-tick stop: reason %q tick %+v after %d ticks", res.Reason, res.Tick, ticks)
	}
}

// closingRun runs a watch whose first tick is quiet, so Run waits out the
// heartbeat and the closing tick returns (r, err).
func closingRun(t *testing.T, r roundtick.Result, tickErr error) (AutopilotResult, int) {
	t.Helper()
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { return nil, nil }, empty, empty)
	calls := 0
	res, err := RunAutopilot(context.Background(), env, Opts{Heartbeat: time.Minute, Poll: time.Minute}, AutopilotOpts{
		Tick: func() (roundtick.Result, error) {
			calls++
			if calls == 1 {
				return roundtick.Result{}, nil
			}
			return r, tickErr
		},
		Poll: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res, calls
}

// #4322: the closing tick is classified by the loop's rule.
func TestAutopilotClassifiesTheClosingTick(t *testing.T) {
	boom := errors.New("boom")
	item := roundtick.Result{New: []roundtick.Item{{Kind: "slot", Target: "ben"}}}
	for _, tc := range []struct {
		name    string
		tick    roundtick.Result
		err     error
		reason  string
		stopErr error
		tickErr error
	}{
		{name: "quiet", reason: ReasonHeartbeat},
		{name: "mid-tick stop", tick: roundtick.Result{Stopped: roundtick.ErrStopped}, reason: ReasonStopped, stopErr: roundtick.ErrStopped},
		{name: "stop error", err: roundtick.ErrNoLease, reason: ReasonStopped, stopErr: roundtick.ErrNoLease},
		{name: "failed", err: boom, reason: ReasonChange, tickErr: boom},
		{name: "new item", tick: item, reason: ReasonChange},
	} {
		res, calls := closingRun(t, tc.tick, tc.err)
		if res.Reason != tc.reason || !errors.Is(res.StopErr, tc.stopErr) || !errors.Is(res.TickErr, tc.tickErr) ||
			(res.Tick == nil) != (tc.err != nil) || res.Waited != time.Minute || calls != 2 {
			t.Errorf("%s: res=%+v calls=%d", tc.name, res, calls)
		}
	}
}

// #4322: an interrupted watch runs no closing tick (a tick can merge and
// assign) and reports the last tick it did run.
func TestAutopilotInterruptRunsNoClosingTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fc := &fakeClock{now: time.Unix(0, 0)}
	env := fc.env(func(time.Duration) (*SlotNews, error) { cancel(); return nil, nil }, empty, empty)
	calls := 0
	res, err := RunAutopilot(ctx, env, Opts{Heartbeat: time.Hour, Poll: time.Minute}, AutopilotOpts{
		Tick: func() (roundtick.Result, error) {
			calls++
			return roundtick.Result{Capped: "first"}, nil
		},
		Poll: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reason != ReasonInterrupt || res.Waited != time.Minute || res.Tick == nil || res.Tick.Capped != "first" || calls != 1 {
		t.Fatalf("res=%+v calls=%d", res, calls)
	}
}
