package roundcfg

import (
	"strings"
	"testing"
	"time"
)

func TestTrainFillSettings(t *testing.T) {
	for _, tc := range []struct {
		cfg  string
		want TrainFill
	}{
		{`{}`, TrainFill{}},
		{`{"round":{"trainMax":3,"trainFillMinutes":60}}`, TrainFill{Max: 3, Minutes: 60}},
		{`{"round":{"trainMax":1}}`, TrainFill{Max: 1}},
		{`{"round":{"trainFillMinutes":1}}`, TrainFill{Minutes: 1}},
	} {
		s, err := Load(project(t, tc.cfg))
		if err != nil || s.TrainFill != tc.want {
			t.Errorf("%s: fill=%+v err=%v, want %+v", tc.cfg, s.TrainFill, err, tc.want)
		}
	}
	for _, bad := range []string{
		`{"round":{"trainMax":-1}}`, `{"round":{"trainFillMinutes":-1}}`,
		`{"round":{"trainMax":"3"}}`, `{"round":{"trainFillMinutes":1.5}}`,
	} {
		if _, err := Load(project(t, bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestTrainFillSingleThresholds(t *testing.T) {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		fill  TrainFill
		count int
		at    time.Time
		want  string
	}{
		{"count only waits", TrainFill{Max: 3}, 2, now.Add(-24 * time.Hour), ""},
		{"count only fires", TrainFill{Max: 3}, 3, time.Time{}, "round.trainMax reached (3 PRs)"},
		{"age only waits", TrainFill{Minutes: 60}, 10, now, ""},
		{"age only fires", TrainFill{Minutes: 60}, 1, now.Add(-time.Hour), "round.trainFillMinutes reached (60 minutes)"},
		{"unknown age", TrainFill{Minutes: 60}, 1, time.Time{}, ""},
		{"future timestamp", TrainFill{Minutes: 60}, 1, now.Add(time.Hour), ""},
		{"no ready PRs", TrainFill{Minutes: 60}, 0, now.Add(-time.Hour), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.fill.Due(tc.count, tc.at, now); got != tc.want {
				t.Errorf("reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTrainFillDecide(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	c := func(id string, age time.Duration) FillCandidate {
		return FillCandidate{ID: id, Ready: now.Add(-age)}
	}
	imm := FillCandidate{ID: "90", Immediate: true}
	for _, tc := range []struct {
		name       string
		fill       TrainFill
		cands      []FillCandidate
		limit      int
		land       []string
		reasonPart string
		wait       string
		holding    string
	}{
		{name: "no candidates", fill: TrainFill{Max: 2}},
		{name: "disabled lone PR holds", fill: TrainFill{}, cands: []FillCandidate{c("1", 0)}},
		{name: "disabled two PRs land", fill: TrainFill{}, cands: []FillCandidate{c("1", 0), c("2", 0)}, land: []string{"1", "2"}},
		{name: "count due", fill: TrainFill{Max: 2, Minutes: 60}, cands: []FillCandidate{c("1", time.Minute), c("2", time.Minute)}, land: []string{"1", "2"}, reasonPart: "round.trainMax", wait: "oldest waited 1m of 60m"},
		{name: "age due", fill: TrainFill{Max: 3, Minutes: 60}, cands: []FillCandidate{c("1", 61*time.Minute), c("2", time.Minute)}, land: []string{"1", "2"}, reasonPart: "round.trainFillMinutes", wait: "oldest waited 61m of 60m"},
		{name: "filling holds", fill: TrainFill{Max: 3, Minutes: 60}, cands: []FillCandidate{c("1", time.Minute), c("2", time.Minute)}, holding: "holding train for main: 2/3 ready, oldest waited 1m of 60m"},
		{name: "unknown ready time cannot fire age", fill: TrainFill{Minutes: 5}, cands: []FillCandidate{{ID: "1"}}, holding: "holding train for main: 1 ready, oldest wait unknown"},
		{name: "immediate lands alone", fill: TrainFill{Max: 4, Minutes: 20}, cands: []FillCandidate{c("1", time.Minute), imm}, land: []string{"90"}, reasonPart: "review disabled", wait: "oldest wait unknown"},
		{name: "due wins over immediate", fill: TrainFill{Max: 2}, cands: []FillCandidate{c("1", time.Minute), imm}, land: []string{"1", "90"}, reasonPart: "round.trainMax", wait: "oldest waited 1m; no time limit"},
		{name: "trim to Max keeps oldest in input order", fill: TrainFill{Max: 2, Minutes: 60}, cands: []FillCandidate{c("1", time.Minute), c("2", 2*time.Minute), c("90", 65*time.Minute)}, land: []string{"2", "90"}, reasonPart: "round.trainMax", wait: "oldest waited 65m of 60m"},
		{name: "limit below Max trims further", fill: TrainFill{Max: 3, Minutes: 60}, cands: []FillCandidate{c("1", time.Minute), c("2", 2*time.Minute), c("3", 3*time.Minute)}, limit: 1, land: []string{"3"}, reasonPart: "round.trainMax", wait: "oldest waited 3m of 60m"},
		{name: "limit above Max defers to Max", fill: TrainFill{Max: 2}, cands: []FillCandidate{c("1", 3*time.Minute), c("2", 2*time.Minute), c("3", time.Minute)}, limit: 9, land: []string{"1", "2"}, reasonPart: "round.trainMax", wait: "oldest waited 3m; no time limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.fill.Decide("main", tc.cands, tc.limit, now)
			if strings.Join(d.Land, ",") != strings.Join(tc.land, ",") {
				t.Errorf("Land = %v, want %v", d.Land, tc.land)
			}
			if !strings.Contains(d.Reason, tc.reasonPart) || (tc.reasonPart == "" && d.Reason != "") {
				t.Errorf("Reason = %q, want it to contain %q", d.Reason, tc.reasonPart)
			}
			if d.Wait != tc.wait || d.Holding != tc.holding {
				t.Errorf("Wait/Holding = %q / %q, want %q / %q", d.Wait, d.Holding, tc.wait, tc.holding)
			}
			if d.Count != len(tc.cands) {
				t.Errorf("Count = %d, want %d", d.Count, len(tc.cands))
			}
		})
	}
}
