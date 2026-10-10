package postgate

import (
	"errors"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/roundcfg"
	"github.com/l4ci/rota/internal/worker"
)

type parked struct{ issues []string }

func (p *parked) park(issue, _ string, _ roundcfg.Settings) error {
	p.issues = append(p.issues, issue)
	return nil
}

func bounces(root, issue string) int {
	return worker.LoadRegistryTolerant(root).Bounces(issue)
}

func TestGatePassEndsTheItemClockAndBestOf(t *testing.T) {
	root := t.TempDir()
	if err := worker.RecordItemStart(root, "12", time.Now()); err != nil {
		t.Fatal(err)
	}
	worker.Update(root, func(d *worker.Doc) { d.SetBestOf(worker.BestOf{Issue: "12"}) })
	// A verdict nobody bounces or lands on keeps the item running.
	if _, err := Gate(root, "12", worker.GateResult{Verdict: "other"}, nil); err != nil {
		t.Fatal(err)
	}
	reg := worker.LoadRegistryTolerant(root)
	if _, ok := reg.ItemStart("12"); !ok || reg.BestOf("12") == nil {
		t.Fatal("a non-pass verdict must keep the clock and the best-of record")
	}
	if _, err := Gate(root, "12", worker.GateResult{Verdict: worker.GatePass}, nil); err != nil {
		t.Fatal(err)
	}
	reg = worker.LoadRegistryTolerant(root)
	if _, ok := reg.ItemStart("12"); ok || reg.BestOf("12") != nil {
		t.Error("a gate pass must clear the item start and the best-of record")
	}
}

func TestGateBouncesAreParkedAtMaxBounces(t *testing.T) {
	root := t.TempDir() // round.maxBounces defaults to 3
	p := &parked{}
	for i := 1; i <= 3; i++ {
		o, err := Gate(root, "7", worker.GateResult{Verdict: worker.GateStale, Slot: "w1", SHA: "sha" + string(rune('a'+i))}, p.park)
		if err != nil {
			t.Fatal(err)
		}
		if o.Bounces != i || o.Parked != (i == 3) {
			t.Fatalf("bounce %d: %+v", i, o)
		}
	}
	if len(p.issues) != 1 || p.issues[0] != "7" {
		t.Fatalf("parked %v", p.issues)
	}
	if n := bounces(root, "7"); n != 0 {
		t.Errorf("parking must clear the count, got %d", n)
	}
}

func TestParkFailureIsReportedAndTheCountStands(t *testing.T) {
	root := t.TempDir()
	worker.RecordBounceIn(nil, root, "7", "w1", "a")
	worker.RecordBounceIn(nil, root, "7", "w1", "b")
	o, err := Gate(root, "7", worker.GateResult{Verdict: worker.GateProvenanceFail, SHA: "c"}, func(string, string, roundcfg.Settings) error { return errors.New("no lease") })
	if err == nil || o.Parked || o.Bounces != 3 {
		t.Fatalf("%+v %v", o, err)
	}
}

// The train counts a bounce like the gate does, for the culprit alone, and
// ends the items it landed.
func TestTrainBouncesTheCulpritAndEndsLandedItems(t *testing.T) {
	root := t.TempDir()
	if err := worker.RecordItemStart(root, "1", time.Now()); err != nil {
		t.Fatal(err)
	}
	r := worker.TrainResult{
		Verdict:     worker.GateStale,
		Culprit:     "w2",
		Members:     []worker.TrainMember{{Target: "w1", Landed: true}, {Target: "w2"}},
		CulpritGate: worker.GateResult{SHA: "abc1234"},
	}
	issues := map[string]string{"w1": "1", "w2": "2"}
	if fails := Train(nil, root, issues, r, (&parked{}).park); len(fails) != 0 {
		t.Fatal(fails)
	}
	if _, ok := worker.LoadRegistryTolerant(root).ItemStart("1"); ok {
		t.Error("a landed member must end its item clock")
	}
	if bounces(root, "2") != 1 || bounces(root, "1") != 0 {
		t.Errorf("bounces: culprit %d, landed %d", bounces(root, "2"), bounces(root, "1"))
	}
	// The same head again is the same refusal, not a second bounce.
	Train(nil, root, issues, r, (&parked{}).park)
	if bounces(root, "2") != 1 {
		t.Errorf("a repeat on the same head counted again: %d", bounces(root, "2"))
	}
}
