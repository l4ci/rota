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
