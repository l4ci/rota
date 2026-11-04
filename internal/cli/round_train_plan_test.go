package cli

import (
	"errors"
	"github.com/l4ci/rota/internal/jsonx"
	"strings"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

func TestTrainNudgeNamesConflictAndLandableSet(t *testing.T) {
	ns := trainNudges([]waitingPR{
		{PR: "#9", Base: "main", Reviewed: true},
		{PR: "#10", Base: "main", Reviewed: true},
		{PR: "#12", Base: "main", Reviewed: true},
	})
	if len(ns) != 1 {
		t.Fatalf("nudges = %+v, want one", ns)
	}
	n := ns[0].withPlan(&worker.TrainPlan{
		Conflicts: []worker.TrainConflict{{A: "9", B: "10", Paths: []string{"a.go"}}},
		Landable:  []string{"9", "12"},
		Blocked:   []worker.TrainBlocked{{Target: "10", By: []string{"9"}}},
	})
	if got, want := n.Command(), "rota worker train 9 12 --base main"; got != want {
		t.Errorf("command = %q, want %q (the conflicting PR must be excluded)", got, want)
	}
	lines := strings.Split(n.Text(), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "nudge\ttrain-conflict\t#9 conflicts with #10 on a.go") ||
		!strings.Contains(lines[0], "land #9 first, then merge main into #10's branch") {
		t.Errorf("conflict line = %q", lines)
	}
	if !strings.Contains(lines[1], "order-blocked: #10 (conflicts with #9)") || !strings.HasPrefix(lines[1], "nudge\ttrain\t") {
		t.Errorf("train line = %q", lines[1])
	}
	o, _ := jsonx.MarshalCompact(trainNudgeData([]trainNudge{n}))
	if !strings.Contains(string(o), `"orderBlocked": [{"pr": "10", "conflictsWith": ["9"]}]`) {
		t.Errorf("json = %s", o)
	}
}

func TestTrainNudgeWithoutConflictsIsUnchanged(t *testing.T) {
	ns := trainNudges([]waitingPR{{PR: "#9", Base: "main", Reviewed: true}, {PR: "#10", Base: "main", Reviewed: true}})
	before := ns[0].Text()
	got := ns[0].withPlan(&worker.TrainPlan{Landable: []string{"9", "10"}})
	if got.Text() != before || strings.Contains(before, "\n") {
		t.Errorf("text changed: %q vs %q", got.Text(), before)
	}
	if ns[0].withPlan(nil).Text() != before {
		t.Error("nil plan changed the nudge")
	}
}

func TestPlanNudgesFailsOpenAndSkipsLoneBases(t *testing.T) {
	c := &Ctx{Deps: &Deps{}}
	ns := trainNudges([]waitingPR{{PR: "#9", Base: "main", Reviewed: true}, {PR: "#10", Base: "main", Reviewed: true}})
	before := ns[0].Text()
	if got := planNudges(c, "/x", ns); got[0].Text() != before {
		t.Error("nil hook changed the nudge")
	}
	calls := 0
	c.Deps.TrainPlan = func(*Ctx, string, string, []string) (*worker.TrainPlan, error) {
		calls++
		return nil, errors.New("base not checked out")
	}
	if got := planNudges(c, "/x", ns); got[0].Text() != before || calls != 1 {
		t.Errorf("error did not fail open: %q calls=%d", got[0].Text(), calls)
	}
	one := trainNudges([]waitingPR{{PR: "#9", Base: "main", Reviewed: true}})
	planNudges(c, "/x", one)
	if calls != 1 {
		t.Errorf("planned a lone PR: calls=%d", calls)
	}
}

// The batch threshold is round.trainMax with round.trainFillMinutes as the
// maximum wait (#2199); the planned nudge keeps their timing.
func TestTrainBatchHoldsUntilCountOrDeadline(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	pr := func(n string, age time.Duration) waitingPR {
		return waitingPR{PR: "#" + n, Base: "main", Reviewed: true, ReadySince: now.Add(-age)}
	}
	fill := roundcfg.TrainFill{Max: 3, Minutes: 60}
	if ns := trainNudgesWithFill([]waitingPR{pr("1", 5*time.Minute), pr("2", 4*time.Minute)}, fill, now); len(ns) != 0 {
		t.Errorf("2 of 3 fresh PRs nudged: %+v", ns)
	}
	if ns := trainNudgesWithFill([]waitingPR{pr("1", 5*time.Minute), pr("2", 4*time.Minute), pr("3", time.Minute)}, fill, now); len(ns) != 1 {
		t.Errorf("3 of 3 did not nudge: %+v", ns)
	}
	if ns := trainNudgesWithFill([]waitingPR{pr("1", 61*time.Minute), pr("2", 4*time.Minute)}, fill, now); len(ns) != 1 {
		t.Errorf("deadline did not nudge: %+v", ns)
	}
}
