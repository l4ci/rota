package limits

import (
	"context"
	"testing"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/worker"
)

type fakeSource struct {
	logins  []string
	meters  []worker.Meter
	picked  string
	pickOK  bool
	exclude []string
}

func (f *fakeSource) Logins(kind string) []string           { return f.logins }
func (f *fakeSource) Meters(context.Context) []worker.Meter { return f.meters }
func (f *fakeSource) Pick(_ context.Context, exclude []string) (string, bool) {
	f.exclude = exclude
	return f.picked, f.pickOK
}

func TestPickAccountMeteredHarnessAsksTheMeterPick(t *testing.T) {
	r := newRig(t)
	src := &fakeSource{picked: "b", pickOK: true, logins: []string{"x"}}
	got, ok := PickAccount(context.Background(), src, r.root, "", "a", t0)
	if !ok || got != "b" || len(src.exclude) != 1 || src.exclude[0] != "a" {
		t.Fatalf("pick %q %v exclude %v", got, ok, src.exclude)
	}
}

func TestPickAccountLoggedHarnessUsesLoginsAndCooling(t *testing.T) {
	r := newRig(t)
	src := &fakeSource{logins: []string{"c1", "c2"}, picked: "never", pickOK: true}
	got, ok := PickAccount(context.Background(), src, r.root, harness.Codex, "c1", t0)
	if !ok || got != "c2" || src.exclude != nil {
		t.Fatalf("pick %q %v exclude %v", got, ok, src.exclude)
	}
	if got, ok := PickAccount(context.Background(), src, r.root, harness.Codex, "c2", t0); !ok || got != "c1" {
		t.Fatalf("pick %q %v", got, ok)
	}
}

func TestMeterReadingMatchesTheAccountByName(t *testing.T) {
	src := &fakeSource{meters: []worker.Meter{{Name: "a", Verdict: worker.VerdictCooling}, {Name: "b", Verdict: worker.VerdictUnknown}}}
	if r := MeterReading(context.Background(), src, "a"); !r.Known || !r.Cooling {
		t.Fatalf("a: %+v", r)
	}
	if r := MeterReading(context.Background(), src, "zz"); r.Known {
		t.Fatalf("unknown account read as known: %+v", r)
	}
}
