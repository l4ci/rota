package roundtick

import (
	"context"
	"testing"
	"time"

	"github.com/l4ci/rota/internal/roundcfg"
)

func TestTickTrainFill(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name               string
		refs               []string
		age                time.Duration
		cap                int
		split, held, mixed bool
		want               int
	}{
		{name: "two fresh wait", refs: []string{"#1", "#2"}},
		{name: "count reached", refs: []string{"#1", "#2", "#3"}, want: 3},
		{name: "lone at deadline", refs: []string{"#1"}, age: time.Hour, want: 1},
		{name: "lone before deadline", refs: []string{"#1"}, age: time.Hour - time.Second},
		{name: "two at deadline", refs: []string{"#1", "#2"}, age: time.Hour, want: 2},
		{name: "oldest of mixed ages", refs: []string{"#2", "#1"}, age: time.Hour, mixed: true, want: 2},
		{name: "bases fill separately", refs: []string{"#1", "#2", "#3"}, split: true},
		{name: "count before tick cap", refs: []string{"#1", "#2", "#3"}, cap: 2, want: 2},
		{name: "held PR does not fill", refs: []string{"#1", "#2", "#3"}, held: true},
		{name: "max bounds train", refs: []string{"#1", "#2", "#3", "#4"}, cap: 5, want: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{queued: tc.refs}
			if tc.split {
				f.baseOf = func(ref string) string { return ref }
			}
			e := f.env()
			e.Cap = tc.cap
			e.TrainFill = roundcfg.TrainFill{Max: 3, Minutes: 60}
			e.Now = func() time.Time { return now }
			e.ReadySince = func(target string) time.Time {
				if tc.mixed && target != "#1" {
					return now
				}
				return now.Add(-tc.age)
			}
			if tc.held {
				e.Held = map[string]string{"#3": "verify-failed"}
			}
			r, err := Run(context.Background(), e)
			if err != nil {
				t.Fatal(err)
			}
			landed := 0
			for _, a := range r.Did {
				if a.Action == "merge" {
					landed++
				}
			}
			t.Logf("ready=%d age=%s landed=%d", len(tc.refs), tc.age, landed)
			if landed != tc.want {
				t.Fatalf("landed %d, want %d; gated=%v trained=%v", landed, tc.want, f.gated, f.trained)
			}
		})
	}
}

func TestTickTrainFillReviewReadiness(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		refs []string
		age  time.Duration
	}{
		{"unready PR cannot fill count", []string{"#1", "#2", "#3"}, 0},
		{"unready PR cannot expire alone", []string{"#3"}, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{queued: tc.refs}
			e := f.env()
			e.TrainFill = roundcfg.TrainFill{Max: 3, Minutes: 60}
			e.Now = func() time.Time { return now }
			e.TrainReady = func(target string) bool { return target != "#3" }
			e.ReadySince = func(string) time.Time { return now.Add(-tc.age) }
			if _, err := Run(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			if len(f.gated) != 0 || len(f.trained) != 0 {
				t.Fatalf("unready PR contributed to landing: gated=%v trained=%v", f.gated, f.trained)
			}
		})
	}
}

func TestTickTrainFillDisabledKeepsLegacyMerge(t *testing.T) {
	f := &fake{queued: []string{"#1"}}
	e := f.env()
	e.TrainReady = func(string) bool {
		t.Fatal("disabled fill policy must not read extra review state")
		return false
	}
	e.ReadySince = func(string) time.Time {
		t.Fatal("disabled fill policy must not read review age")
		return time.Time{}
	}
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(f.gated) != 1 || f.gated[0] != "main:#1" {
		t.Fatalf("gated=%v, want legacy lone gate", f.gated)
	}
}

func TestTickTrainFillReportsChangesAcrossRestart(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 6, 0, 0, time.UTC)
	at := now.Add(-6 * time.Minute)
	f := &fake{queued: []string{"#1", "#2"}}
	e := f.env()
	e.TrainFill = roundcfg.TrainFill{Max: 4, Minutes: 20}
	e.Now = func() time.Time { return now }
	e.ReadySince = func(string) time.Time { return at }
	cd := t.TempDir()
	tick := func(want string) {
		t.Helper()
		LoadState(cd, 40).Apply(&e)
		r, err := Run(context.Background(), e)
		if err != nil {
			t.Fatal(err)
		}
		if want == "" && len(r.Holding) != 0 || want != "" && (len(r.Holding) != 1 || r.Holding[0] != want) {
			t.Fatalf("holding=%q, want %q", r.Holding, want)
		}
		if len(r.NeedsYou) != 0 || len(r.Did) != 0 {
			t.Fatalf("fill wait became actionable: %+v", r)
		}
		if err := SaveState(cd, 40, r); err != nil {
			t.Fatal(err)
		}
	}
	tick("holding train for main: 2/4 ready, oldest waited 6m of 20m")
	tick("")
	now = now.Add(time.Minute)
	tick("")
	f.queued = []string{"#1"}
	tick("holding train for main: 1/4 ready, oldest waited 7m of 20m")
	if key := LoadState(cd, 40).Filling["main"]; key != "main:1/4" {
		t.Fatalf("persisted fill key=%q, want base/count/max without elapsed time", key)
	}
}

func TestFillBypassReachesGateWithReviewNotReady(t *testing.T) {
	f := &fake{queued: []string{"#11"}}
	e := f.env()
	e.TrainFill = roundcfg.TrainFill{Max: 4}
	e.TrainReady = func(string) bool { return false }
	e.FillBypass = func(string) bool { return true }
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(f.gated) != 1 || f.gated[0] != "main:#11" {
		t.Fatalf("state refusal did not reach gate: %v", f.gated)
	}
}

func TestTickTrainFillKeepsOldestReady(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	f := &fake{queued: []string{"#1", "#2", "#90"}}
	e := f.env()
	e.TrainFill = roundcfg.TrainFill{Max: 2, Minutes: 60}
	e.Now = func() time.Time { return now }
	e.ReadySince = func(target string) time.Time {
		switch target {
		case "#90":
			return now.Add(-65 * time.Minute)
		case "#2":
			return now.Add(-2 * time.Minute)
		default:
			return now.Add(-time.Minute)
		}
	}
	if _, err := Run(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if len(f.trained) != 1 || len(f.trained[0]) != 3 || f.trained[0][1] != "#2" || f.trained[0][2] != "#90" {
		t.Fatalf("oldest-ready selection: %v", f.trained)
	}
}
