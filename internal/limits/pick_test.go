package limits

import (
	"context"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/harness"
	"github.com/l4ci/rota/internal/usagelimit"
)

type fakeSource struct {
	logins  []string
	meters  []usagelimit.Meter
	picked  string
	pickOK  bool
	exclude []string
}

func (f *fakeSource) Logins(kind string) []string               { return f.logins }
func (f *fakeSource) Meters(context.Context) []usagelimit.Meter { return f.meters }
func (f *fakeSource) Pick(_ context.Context, exclude []string) (string, bool) {
	f.exclude = exclude
	return f.picked, f.pickOK
}

func TestPickAccountMeteredHarnessAsksTheMeterPick(t *testing.T) {
	src := &fakeSource{picked: "b", pickOK: true, logins: []string{"x"}}
	got, ok := PickAccount(context.Background(), src, nil, "", "a", t0)
	if !ok || got != "b" || len(src.exclude) != 1 || src.exclude[0] != "a" {
		t.Fatalf("pick %q %v exclude %v", got, ok, src.exclude)
	}
}

func TestPickAccountLoggedHarnessUsesLoginsAndCooling(t *testing.T) {
	src := &fakeSource{logins: []string{"c1", "cooling", "c2"}, picked: "never", pickOK: true}
	entries := []Entry{{Session: "ben", Kind: harness.Codex, Account: "cooling", Status: StatusWaiting, ResetsAt: Time(t0.Add(time.Hour))}}
	got, ok := PickAccount(context.Background(), src, entries, harness.Codex, "c1", t0)
	if !ok || got != "c2" || src.exclude != nil {
		t.Fatalf("pick %q %v exclude %v", got, ok, src.exclude)
	}
	if got, ok := PickAccount(context.Background(), src, entries, harness.Codex, "c2", t0); !ok || got != "c1" {
		t.Fatalf("pick %q %v", got, ok)
	}
}

func TestMeterReadingMatchesTheAccountByName(t *testing.T) {
	src := &fakeSource{meters: []usagelimit.Meter{{Name: "a", Verdict: usagelimit.VerdictCooling}, {Name: "b", Verdict: usagelimit.VerdictUnknown}}}
	if r := MeterReading(context.Background(), src, "a"); !r.Known || !r.Cooling {
		t.Fatalf("a: %+v", r)
	}
	if r := MeterReading(context.Background(), src, "zz"); r.Known {
		t.Fatalf("unknown account read as known: %+v", r)
	}
}
