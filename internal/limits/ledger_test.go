package limits

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/ledger"
)

func TestLimitLedgerLimitedAndRerouted(t *testing.T) {
	r := slotRig(t)
	r.build()
	r.step(true)
	es, err := ledger.Load(r.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 || es[0].Kind != ledger.KindLimited || es[1].Kind != ledger.KindRerouted {
		t.Fatalf("entries %+v", es)
	}
	l, rr := es[0], es[1]
	if l.Slot != "ben" || l.Issue != "67" || l.Account != "a" || l.DetailStr("resetsAt") != Time(t0.Add(2*time.Hour)) {
		t.Errorf("limited = %+v", l)
	}
	if rr.Slot != "dana" || rr.Issue != "67" || rr.DetailStr("from") != "ben" || rr.Account != "b" {
		t.Errorf("rerouted = %+v", rr)
	}
}

func TestLimitLedgerSleepIsOnlyLimited(t *testing.T) {
	r := slotRig(t)
	r.set.Mode = ModeSleep
	r.build()
	r.step(true)
	es, _ := ledger.Load(r.root)
	if len(es) != 1 || es[0].Kind != ledger.KindLimited {
		t.Fatalf("entries %+v", es)
	}
}
