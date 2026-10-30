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
	gone := errors.New("lease gone")
	run := func(err error) AutopilotResult {
		res, rerr := RunAutopilot(context.Background(), env, Opts{Heartbeat: time.Minute, Poll: time.Minute}, AutopilotOpts{
			Tick:    func() (roundtick.Result, error) { return roundtick.Result{}, err },
			Stopped: func(e error) bool { return e == gone },
			Poll:    time.Minute,
		})
		if rerr != nil {
			t.Fatal(rerr)
		}
		return res
	}
	if res := run(gone); res.Reason != ReasonStopped || res.Tick != nil {
		t.Fatalf("stop: %+v", res)
	}
	boom := errors.New("boom")
	if res := run(boom); res.Reason != ReasonChange || res.TickErr != boom || res.Tick != nil {
		t.Fatalf("tick failure: %+v", res)
	}
}
