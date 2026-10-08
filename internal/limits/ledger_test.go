package limits

import (
	"testing"
	"time"

	"github.com/l4ci/rota/internal/ledger"
)

// A reroute is the limits watcher calling `round transfer`, which records the
// transfer itself: the watcher adds only the limited entry.
func TestLimitLedgerLimited(t *testing.T) {
	r := slotRig(t)
	r.build()
	r.step(true)
	es, err := ledger.Load(r.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].Kind != ledger.KindLimited {
		t.Fatalf("entries %+v", es)
	}
	l := es[0]
	if l.Slot != "ben" || l.Issue != "67" || l.Account != "a" || l.DetailStr("resetsAt") != Time(t0.Add(2*time.Hour)) {
		t.Errorf("limited = %+v", l)
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
