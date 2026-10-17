package roundtick

import (
	"slices"
	"testing"

	"github.com/l4ci/rota/internal/jsonx"
	"github.com/l4ci/rota/internal/round"
	"github.com/l4ci/rota/internal/worker"
)

func slotObj(name, state, pr string) *worker.Slot {
	o := jsonx.NewObject()
	o.Set("name", name)
	o.Set("state", state)
	o.Set("pr", pr)
	return worker.AsSlot(o)
}

func TestSlotsOfLowercasesStateAndCarriesPR(t *testing.T) {
	got := SlotsOf([]*worker.Slot{slotObj("ben", "DONE", "#7"), slotObj("dana", "idle", "")})
	if len(got) != 2 || got[0].Name != "ben" || got[0].State != "done" || got[0].PR != "#7" || got[1].State != "idle" {
		t.Fatalf("SlotsOf = %+v", got)
	}
}

func TestQueuedRefsSkipsRecordsWithoutAPR(t *testing.T) {
	got := QueuedRefs([]worker.QueuedPR{{PR: "#1"}, {Issue: "9"}, {PR: "#3"}})
	if !slices.Equal(got, []string{"#1", "#3"}) {
		t.Fatalf("QueuedRefs = %v", got)
	}
}

func TestReconcileLinesNameKindAndKey(t *testing.T) {
	out := round.Outcome{
		Repaired: []round.Finding{{Kind: "dead-tab", Slot: "ben"}},
		Drift:    []round.Finding{{Kind: "pr-missing", Issue: "42"}},
	}
	rep, drift := ReconcileLines(out)
	if !slices.Equal(rep, []string{"dead-tab ben"}) || len(drift) != 1 || drift[0] != "pr-missing "+out.Drift[0].Key() {
		t.Fatalf("repaired=%v drift=%v", rep, drift)
	}
}

func TestAssignedAgentsListsBothBestOfSlots(t *testing.T) {
	one := AssignedAgents(round.Assigned{Agent: "kit"})
	two := AssignedAgents(round.Assigned{Agent: "x", BestOf: []round.Assigned{{Agent: "ben"}, {Agent: "nia"}}})
	if !slices.Equal(one, []string{"kit"}) || !slices.Equal(two, []string{"ben", "nia"}) {
		t.Fatalf("one=%v two=%v", one, two)
	}
}
