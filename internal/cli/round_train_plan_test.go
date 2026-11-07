package cli

import (
	"errors"
	"github.com/l4ci/rota/internal/jsonx"
	"strings"
	"testing"

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
	c := &Ctx{Deps: &Deps{Git: fakeHeads(map[string]string{})}}
	ns := trainNudges([]waitingPR{{PR: "#9", Base: "main", Reviewed: true}, {PR: "#10", Base: "main", Reviewed: true}})
	before := ns[0].Text()
	if got := planNudges(c, t.TempDir(), ns); got[0].Text() != before {
		t.Error("nil hook changed the nudge")
	}
	calls := 0
	c.Deps.TrainPlan = func(*Ctx, string, string, []string) (*worker.TrainPlan, error) {
		calls++
		return nil, errors.New("base not checked out")
	}
	if got := planNudges(c, t.TempDir(), ns); got[0].Text() != before || calls != 1 {
		t.Errorf("error did not fail open: %q calls=%d", got[0].Text(), calls)
	}
	one := trainNudges([]waitingPR{{PR: "#9", Base: "main", Reviewed: true}})
	planNudges(c, t.TempDir(), one)
	if calls != 1 {
		t.Errorf("planned a lone PR: calls=%d", calls)
	}
}
